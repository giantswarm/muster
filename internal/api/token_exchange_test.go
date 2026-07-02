package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithResolvedRuntime_DoesNotMutateShared is a regression test for the
// MCPServer restart-churn bug (giantswarm/giantswarm#37060): stamping the
// per-connection runtime state must not mutate the shared registry
// definition's config, because MCPServerReconciler.ConfigurationChanged
// compares it against the CR. Covers the requiredAudiences case, which the
// credential-only cases missed.
func TestWithResolvedRuntime_DoesNotMutateShared(t *testing.T) {
	base := &TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example/token",
		ConnectorID:      "giantswarm-simple-oidc",
		Scopes:           "openid profile email groups",
	}

	cfg, err := base.WithResolvedRuntime(
		"resolved-client-id",
		"resolved-client-secret",
		[]string{"dex-k8s-authenticator"},
	)
	require.NoError(t, err)

	// The returned copy carries the runtime state...
	assert.Equal(t, "resolved-client-id", cfg.ClientID)
	assert.Equal(t, "resolved-client-secret", cfg.ClientSecret)
	assert.Contains(t, cfg.Scopes, "openid profile email groups")
	assert.Contains(t, cfg.Scopes, "dex-k8s-authenticator",
		"required audiences should be appended as cross-client scopes on the copy")

	// ...while the shared base is left completely untouched.
	assert.Empty(t, base.ClientID, "shared definition ClientID must not be mutated")
	assert.Empty(t, base.ClientSecret, "shared definition ClientSecret must not be mutated")
	assert.Equal(t, "openid profile email groups", base.Scopes,
		"shared definition Scopes must not gain audience scopes (would cause reconcile churn)")
}

// TestWithResolvedRuntime_NoAudiences covers the plain tokenExchange case
// (no requiredAudiences): scopes are carried through unchanged and the shared
// base is not mutated.
func TestWithResolvedRuntime_NoAudiences(t *testing.T) {
	base := &TokenExchangeConfig{
		Enabled: true,
		Scopes:  "openid profile",
	}

	cfg, err := base.WithResolvedRuntime("cid", "secret", nil)
	require.NoError(t, err)

	assert.Equal(t, "openid profile", cfg.Scopes)
	assert.Equal(t, "cid", cfg.ClientID)
	assert.Empty(t, base.ClientID, "shared definition must not be mutated")
	assert.Equal(t, "openid profile", base.Scopes)
}

// TestSpecOnly clears exactly the runtime-resolved fields and leaves every
// spec field (including the runtime-derived scope string's spec value) intact,
// without mutating the receiver.
func TestSpecOnly(t *testing.T) {
	base := &TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example/token",
		ConnectorID:      "giantswarm-simple-oidc",
		Scopes:           "openid profile",
		ClientID:         "resolved-client-id",
		ClientSecret:     "resolved-client-secret",
	}

	spec := base.SpecOnly()

	assert.Empty(t, spec.ClientID)
	assert.Empty(t, spec.ClientSecret)
	assert.Equal(t, "https://dex.example/token", spec.DexTokenEndpoint)
	assert.Equal(t, "giantswarm-simple-oidc", spec.ConnectorID)
	assert.Equal(t, "openid profile", spec.Scopes)
	assert.True(t, spec.Enabled)

	assert.Equal(t, "resolved-client-id", base.ClientID, "receiver must not be mutated")
	assert.Equal(t, "resolved-client-secret", base.ClientSecret, "receiver must not be mutated")
}
