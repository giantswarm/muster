package oauth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
)

// IsOAuthUnauthorizedError recognizes a 401 in every shape mcp-go reports one:
// the SSE transport's ErrUnauthorized, the streamable HTTP transport's
// AuthorizationRequiredError (which unwraps to ErrAuthorizationRequired) in
// the transport.Error the client wraps it in, the OAuth transport's typed
// error, and a token store that holds nothing to send.
func TestIsOAuthUnauthorizedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"SSE bare 401", fmt.Errorf("request failed: %w", transport.ErrUnauthorized), true},
		{"streamable HTTP bare 401 as the client wraps it", fmt.Errorf("failed to call tool: %w", transport.NewError(&transport.AuthorizationRequiredError{})), true},
		{"streamable HTTP bare 401 sentinel", fmt.Errorf("x: %w", transport.ErrAuthorizationRequired), true},
		{"OAuth transport 401", &transport.OAuthAuthorizationRequiredError{}, true},
		{"no token in the store", fmt.Errorf("x: %w", transport.ErrOAuthAuthorizationRequired), true},
		{"another error", errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsOAuthUnauthorizedError(tt.err))
		})
	}
}
