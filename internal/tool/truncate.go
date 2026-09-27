package tool

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// markerReserve is the byte budget set aside for the truncation marker so
// that a truncated result stays within its limit, marker included.
const markerReserve = 160

// Truncate bounds s to about limit bytes by keeping its head and tail and
// replacing the middle with a marker that says how much was cut.
//
// Head+tail rather than head-only: for a command's output the end (the
// summary line, the failing assertion, the exit status) is usually the most
// informative part, and for a file the model at least sees where it ends.
// Cuts land on line boundaries when one is near, and never inside a UTF-8
// sequence. limit <= 0 disables truncation.
func Truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	budget := limit - markerReserve
	if budget < 2 {
		budget = 2
	}
	headLen := budget / 2
	tailLen := budget - headLen

	head := trimToLineEnd(s[:headLen])
	tail := trimToLineStart(s[len(s)-tailLen:])
	omitted := s[len(head) : len(s)-len(tail)]
	return joinTruncated(head, tail, len(omitted), strings.Count(omitted, "\n"))
}

// joinTruncated assembles head + marker + tail. Shared by Truncate and
// BoundedBuffer so a truncated string and a truncated stream look the same.
func joinTruncated(head, tail string, omittedBytes, omittedLines int) string {
	var b strings.Builder
	b.Grow(len(head) + len(tail) + markerReserve)
	b.WriteString(head)
	if !strings.HasSuffix(head, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n... [output truncated: %d bytes (%d lines) omitted; showing the first %d and last %d bytes] ...\n\n",
		omittedBytes, omittedLines, len(head), len(tail))
	b.WriteString(tail)
	return b.String()
}

// trimToLineEnd shortens s to its last line boundary when one is within the
// final quarter, and otherwise to a UTF-8 boundary.
func trimToLineEnd(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= len(s)*3/4 {
		return s[:i+1]
	}
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// trimToLineStart drops the partial first line of s when a boundary is
// within the first quarter, and otherwise any leading UTF-8 continuation
// bytes.
func trimToLineStart(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)/4 {
		return s[i+1:]
	}
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// BoundedBuffer is an io.Writer that retains only the head and tail of what
// is written to it, within a byte limit, so that a subprocess producing
// gigabytes cannot exhaust memory while its output is being captured. Its
// String renders the same layout as [Truncate].
type BoundedBuffer struct {
	limit   int
	headCap int
	tailCap int
	head    []byte
	tail    []byte
	total   int
	// newlines counts every '\n' ever written, so the marker can report
	// how many lines fell into the dropped middle.
	newlines int
}

// NewBoundedBuffer returns a buffer that renders to at most about limit
// bytes. limit <= 0 means unbounded.
func NewBoundedBuffer(limit int) *BoundedBuffer {
	b := &BoundedBuffer{limit: limit}
	if limit > 0 {
		budget := limit - markerReserve
		if budget < 2 {
			budget = 2
		}
		b.headCap = budget / 2
		b.tailCap = budget - b.headCap
	}
	return b
}

// Write implements io.Writer. It never fails and never blocks.
func (b *BoundedBuffer) Write(p []byte) (int, error) {
	// io.Writer's contract: report len(p) consumed, whatever was kept —
	// a short count would make exec's copier fail the command with
	// io.ErrShortWrite.
	n := len(p)
	b.total += n
	b.newlines += bytes.Count(p, []byte{'\n'})
	if b.limit <= 0 {
		b.head = append(b.head, p...)
		return n, nil
	}
	if room := b.headCap - len(b.head); room > 0 {
		k := min(room, len(p))
		b.head = append(b.head, p[:k]...)
		p = p[k:]
	}
	if len(p) == 0 {
		return n, nil
	}
	b.tail = append(b.tail, p...)
	// Amortised: let the tail grow to twice its cap before compacting,
	// so that a stream of tiny writes does not copy on every call.
	if len(b.tail) > 2*b.tailCap {
		b.tail = append(b.tail[:0], b.tail[len(b.tail)-b.tailCap:]...)
	}
	return n, nil
}

// Len returns the number of bytes written so far, including omitted ones.
func (b *BoundedBuffer) Len() int { return b.total }

// String renders the retained output, with a truncation marker if anything
// was dropped.
func (b *BoundedBuffer) String() string {
	if b.limit <= 0 || b.total <= b.headCap+b.tailCap {
		return string(b.head) + string(b.tail)
	}
	tail := b.tail
	if len(tail) > b.tailCap {
		tail = tail[len(tail)-b.tailCap:]
	}
	head := trimToLineEnd(string(b.head))
	tailStr := trimToLineStart(string(tail))
	omittedBytes := b.total - len(head) - len(tailStr)
	omittedLines := b.newlines - strings.Count(head, "\n") - strings.Count(tailStr, "\n")
	return joinTruncated(head, tailStr, omittedBytes, omittedLines)
}
