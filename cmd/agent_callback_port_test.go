package cmd

import (
	"testing"

	"github.com/giantswarm/muster/internal/cli"
)

// TestAgentCallbackPort_HonorsEnvVar is the regression test for cmd/agent.go:294,
// which hardcodes the OAuth callback port:
//
//	oauth.NewAuthManager(oauth.AuthManagerConfig{
//	    CallbackPort: cli.DefaultCallbackPort,   // cmd/agent.go:24 — const 3000
//
// MUSTER_OAUTH_CALLBACK_PORT is read in exactly one place, internal/cli's
// getCallbackPort (auth_adapter.go:151). The agent MCP-server path never consults
// it, and that directly-constructed AuthManager is what drives the OAuth flow in
// `muster agent --mcp-server`.
//
// User-visible effect: with port 3000 occupied, MUSTER_OAUTH_CALLBACK_PORT=8081
// makes `muster auth login` work while `muster agent --mcp-server` still binds
// 3000 and fails — and the port-in-use message at auth_adapter.go:332-336
// interpolates getCallbackPort(), so it can name 8081 while the code that
// actually failed used 3000.
func TestAgentCallbackPort_HonorsEnvVar(t *testing.T) {
	t.Run("falls back to the default when unset", func(t *testing.T) {
		t.Setenv("MUSTER_OAUTH_CALLBACK_PORT", "")
		if got := agentCallbackPort(); got != cli.DefaultCallbackPort {
			t.Errorf("agentCallbackPort() = %d, want default %d", got, cli.DefaultCallbackPort)
		}
	})

	t.Run("honors MUSTER_OAUTH_CALLBACK_PORT", func(t *testing.T) {
		t.Setenv("MUSTER_OAUTH_CALLBACK_PORT", "8081")
		if got := agentCallbackPort(); got != 8081 {
			t.Errorf("agentCallbackPort() = %d, want 8081: the agent path ignores the env var that `muster auth login` honors", got)
		}
	})

	t.Run("falls back on an unparseable value", func(t *testing.T) {
		t.Setenv("MUSTER_OAUTH_CALLBACK_PORT", "not-a-port")
		if got := agentCallbackPort(); got != cli.DefaultCallbackPort {
			t.Errorf("agentCallbackPort() = %d, want default %d", got, cli.DefaultCallbackPort)
		}
	})

	t.Run("falls back on an out-of-range value", func(t *testing.T) {
		t.Setenv("MUSTER_OAUTH_CALLBACK_PORT", "70000")
		if got := agentCallbackPort(); got != cli.DefaultCallbackPort {
			t.Errorf("agentCallbackPort() = %d, want default %d", got, cli.DefaultCallbackPort)
		}
	})
}
