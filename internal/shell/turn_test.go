package shell

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestExplain(t *testing.T) {
	auth := agentapi.NewError(agentapi.ErrAuth, "Unauthorized")
	if got := Explain(auth); !strings.Contains(got, "Unauthorized") || !strings.Contains(got, "api_key") {
		t.Errorf("Explain(auth) = %q; want the error and what to check", got)
	}
	other := agentapi.NewError(agentapi.ErrNetwork, "connection refused")
	if got := Explain(other); got != other.Error() {
		t.Errorf("Explain(network) = %q; want the bare error", got)
	}
	if got := Explain(errors.New("plain")); got != "plain" {
		t.Errorf("Explain(plain) = %q", got)
	}
}
