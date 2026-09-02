package cmd

import (
	"testing"

	"github.com/giantswarm/muster/internal/cli"
)

// TestAgentCallbackPort_HonorsEnvVar pins agentCallbackPort to cli.GetCallbackPort,
// so that every OAuth entry point resolves MUSTER_OAUTH_CALLBACK_PORT the same way.
// A second resolver in cmd is how `muster auth login` and `muster agent
// --mcp-server` come to disagree about which port to bind: with 3000 occupied and
// the variable set to 8081, login binds 8081 and works while the agent binds 3000
// and fails, and cli.AuthAdapter's port-in-use message names the port it resolved
// rather than the one the failing code used.
//
// Scope: this covers the resolver only. runMCPServerWithOAuth passes the result
// to oauth.NewAuthManager, and nothing here observes that argument -- pinning it
// would need a seam in the construction. Read this as a guard on the helper, not
// on its call site.
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
