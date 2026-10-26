package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/reconciler"
)

// TestRefusalsSurviveTheStatusSanitizer: a refusal reaches the MCPServer's
// status.lastError through reconciler.SanitizeErrorMessage, which redacts the
// word after "secret:" as a value. The refusal must read the same afterwards.
func TestRefusalsSurviveTheStatusSanitizer(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("github-token"), WithSecretBindingMode(api.SecretBindingEnforce))
	registerSecretKeyReader(t, adapter)

	_, err := ResolveHeaders(context.Background(), githubServer, nil, []api.MCPServerHeaderFrom{bearerFrom("github-token")})
	require.ErrorIs(t, err, api.ErrSecretNotBound)

	sanitized := reconciler.SanitizeErrorMessage(err.Error())
	assert.NotContains(t, sanitized, "[REDACTED]")
	assert.Contains(t, sanitized, "MCPServer agent-platform/github may not read Secret agent-platform/github-token")
	assert.Contains(t, sanitized, "add github to the Secret's annotation "+api.MCPServerSecretAnnotation)
}
