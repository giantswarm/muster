package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
)

// TestCheckForAuthRequiredError_recognisesAllTransportSentinels guards against
// the regression seen in muster#579/graveler: mcp-go v0.49.0 introduced
// transport.ErrAuthorizationRequired, which this helper did not recognise,
// causing auth-required servers to stay in Failed state instead of being
// registered as pending-auth. Any future sentinel the transport adds for 401
// should be added here as well.
func TestCheckForAuthRequiredError_recognisesAllTransportSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ErrUnauthorized raw", transport.ErrUnauthorized},
		{"ErrUnauthorized wrapped", fmt.Errorf("transport error: %w", transport.ErrUnauthorized)},
		{"ErrAuthorizationRequired raw", transport.ErrAuthorizationRequired},
		{"ErrAuthorizationRequired wrapped", fmt.Errorf("failed to initialize MCP protocol: %w", transport.ErrAuthorizationRequired)},
		{"ErrOAuthAuthorizationRequired raw", transport.ErrOAuthAuthorizationRequired},
		{"ErrOAuthAuthorizationRequired wrapped", fmt.Errorf("mcp init: %w", transport.ErrOAuthAuthorizationRequired)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckForAuthRequiredError(context.Background(), tc.err, "https://example/mcp")
			if got == nil {
				t.Fatalf("expected *AuthRequiredError, got nil for %v", tc.err)
			}
			if got.URL != "https://example/mcp" {
				t.Fatalf("URL not preserved: %q", got.URL)
			}
		})
	}
}

func TestCheckForAuthRequiredError_ignoresUnrelatedErrors(t *testing.T) {
	cases := []error{
		errors.New("connection refused"),
		fmt.Errorf("dial tcp: %w", errors.New("timeout")),
		errors.New("authorization required but unrelated"),
	}
	for _, err := range cases {
		if got := CheckForAuthRequiredError(context.Background(), err, ""); got != nil {
			t.Fatalf("expected nil for %v, got %+v", err, got)
		}
	}
}

// TestCheckForAuthRequiredError_reachesThroughDoubleWrap covers the exact shape
// of the production error we hit: client.Initialize wraps the transport
// sentinel, then the aggregator wraps that, then the orchestrator wraps again.
func TestCheckForAuthRequiredError_reachesThroughDoubleWrap(t *testing.T) {
	wrapped := fmt.Errorf("failed to start service: %w",
		fmt.Errorf("failed to initialize streamable-http MCP client: %w",
			fmt.Errorf("failed to initialize MCP protocol: %w", transport.ErrAuthorizationRequired)))

	got := CheckForAuthRequiredError(context.Background(), wrapped, "https://example/mcp")
	if got == nil {
		t.Fatal("expected detection to survive three wrapping layers")
	}
}
