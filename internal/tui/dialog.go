package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// dialog is an open permission question. It owns a viewport for the
// evidence — a diff can be longer than the screen — and answers once.
type dialog struct {
	req   agentapi.PermissionRequest
	reply chan<- permission.Decision
	vp    viewport.Model
}

// dialogFrame is the lines the box spends around the evidence: border,
// title, path, blank, footer.
const dialogFrame = 6

func newDialog(req agentapi.PermissionRequest, reply chan<- permission.Decision, w, h int, sty styles) *dialog {
	body := req.Detail
	if req.Class == agentapi.ClassWrite {
		body = colorDiff(body, sty)
	}
	inner := max(h-dialogFrame, 1)
	vp := viewport.New(viewport.WithWidth(max(w-4, 1)), viewport.WithHeight(inner))
	vp.SetContent(body)
	return &dialog{req: req, reply: reply, vp: vp}
}

// key handles one key press in the dialog. It reports whether the
// dialog answered and is done.
func (d *dialog) key(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "y", "Y":
		d.answer(permission.Allow)
		return true
	case "n", "N", "esc":
		d.answer(permission.Deny)
		return true
	case "a", "A":
		if d.req.Always == "" {
			return false
		}
		d.answer(permission.AllowAlways)
		return true
	case "pgdown", "down", "j":
		d.vp.ScrollDown(d.scrollStep(k))
	case "pgup", "up", "k":
		d.vp.ScrollUp(d.scrollStep(k))
	}
	return false
}

func (d *dialog) scrollStep(k tea.KeyPressMsg) int {
	if strings.HasPrefix(k.String(), "pg") {
		return d.vp.Height()
	}
	return 1
}

func (d *dialog) answer(dec permission.Decision) {
	d.reply <- dec
}

// view draws the dialog in a w×h box.
func (d *dialog) view(w, h int, sty styles) string {
	title := sty.dialogTitle.Render("permission: " + d.req.Summary)
	path := ""
	if d.req.Path != "" {
		path = sty.hint.Render(d.req.Path)
	}
	options := sty.dialogKey.Render("y") + " allow · " + sty.dialogKey.Render("n") + " deny"
	if d.req.Always != "" {
		options += " · " + sty.dialogKey.Render("a") + " always (" + d.req.Always + ")"
	}
	if d.vp.TotalLineCount() > d.vp.Height() {
		options += " · pgup/pgdn scroll"
	}
	body := strings.Join([]string{title, path, "", d.vp.View(), sty.hint.Render(options)}, "\n")
	box := sty.dialogBox.Width(max(w-2, 1)).Render(body)
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, box)
}
