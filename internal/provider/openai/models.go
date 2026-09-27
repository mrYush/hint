package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// modelsPath is the listing endpoint of the dialect, a sibling of
// /chat/completions under the profile's base URL.
const modelsPath = "/models"

// Model is one entry of GET /models as the dialect reports it. Gateways
// differ in what they fill beyond the id, so only the id is guaranteed.
type Model struct {
	// ID is the name usable as a profile's model.
	ID string
	// Created is when the provider says the model was made; zero when the
	// server sent nothing.
	Created time.Time
	// OwnedBy is the provider's ownership label, e.g. "openai"; often empty
	// on gateways.
	OwnedBy string
}

// Models lists the models the endpoint serves, in the server's order. The
// call carries the profile's key like a chat request; a failure is an
// *agentapi.Error classified the same way (a 401 is ErrAuth, a refused
// connection ErrNetwork), so `hint models` reports it in one line.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	url := modelsURL(c.profile.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, c.attributed(agentapi.WrapError(agentapi.ErrInvalidRequest, err, "build models request"))
	}
	req.Header.Set("Accept", "application/json")
	if c.profile.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.profile.APIKey)
	}
	c.debugf("GET %s", url)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.attributed(transportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.attributed(statusError(resp))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, c.attributed(agentapi.WrapError(agentapi.ErrNetwork, err, "read models response"))
	}
	var wire struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, c.attributed(agentapi.WrapError(agentapi.ErrInvalidRequest, err, "decode models response"))
	}
	models := make([]Model, 0, len(wire.Data))
	for _, m := range wire.Data {
		if m.ID == "" {
			continue
		}
		out := Model{ID: m.ID, OwnedBy: m.OwnedBy}
		if m.Created > 0 {
			out.Created = time.Unix(m.Created, 0).UTC()
		}
		models = append(models, out)
	}
	return models, nil
}

// modelsURL derives the listing URL from the base, tolerating a base that
// already names the completions path (the same leniency as completionsURL).
func modelsURL(base string) string {
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, completionsPath)
	return fmt.Sprintf("%s%s", base, modelsPath)
}
