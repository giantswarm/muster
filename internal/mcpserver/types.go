package mcpserver

import (
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/client/transport"
)

// McpDiscreteStatusUpdate is used to report discrete status changes from a running MCP process.
// It focuses on the state, not verbose logs.
type McpDiscreteStatusUpdate struct {
	Name          string // The unique label of the MCP server instance
	ProcessStatus string // A string indicating the process status, e.g., "ProcessInitializing", "ProcessStarting", "ProcessRunning", "ProcessExitedWithError"
	ProcessErr    error  // The actual Go error object if the process failed or exited with an error
}

// McpUpdateFunc is a callback function type for receiving McpDiscreteStatusUpdate messages.
type McpUpdateFunc func(update McpDiscreteStatusUpdate)

// AuthInfo contains OAuth authentication information extracted from
// a 401 response during MCP server initialization.
type AuthInfo struct {
	// Issuer is the OAuth issuer URL (from WWW-Authenticate realm)
	Issuer string

	// Scope is the OAuth scope required by the server
	Scope string

	// ResourceMetadataURL is the URL to fetch OAuth metadata (MCP-specific)
	ResourceMetadataURL string
}

// AuthRequiredError is returned when an MCP server requires OAuth authentication
// before the protocol handshake can complete. This error contains the information
// needed to initiate the OAuth flow.
type AuthRequiredError struct {
	// URL is the endpoint that returned the 401
	URL string

	// AuthInfo contains the OAuth parameters extracted from the 401 response
	AuthInfo AuthInfo

	// Err is the underlying error
	Err error
}

// Error implements the error interface
func (e *AuthRequiredError) Error() string {
	return "authentication required: " + e.Err.Error()
}

// Unwrap returns the underlying error
func (e *AuthRequiredError) Unwrap() error {
	return e.Err
}

// HasValidChallenge returns true if the error contains valid auth challenge information.
func (e *AuthRequiredError) HasValidChallenge() bool {
	if e == nil {
		return false
	}
	return e.AuthInfo.Issuer != "" || e.AuthInfo.ResourceMetadataURL != ""
}

// GetIssuer returns the OAuth issuer URL from the error.
func (e *AuthRequiredError) GetIssuer() string {
	if e == nil {
		return ""
	}
	return e.AuthInfo.Issuer
}

// GetScope returns the OAuth scope from the error.
func (e *AuthRequiredError) GetScope() string {
	if e == nil {
		return ""
	}
	return e.AuthInfo.Scope
}

// GetResourceMetadataURL returns the resource metadata URL from the error.
func (e *AuthRequiredError) GetResourceMetadataURL() string {
	if e == nil {
		return ""
	}
	return e.AuthInfo.ResourceMetadataURL
}

// CheckForAuthRequiredError examines an error to determine if it's a 401 authentication
// required error. It uses mcp-go's typed error detection instead of string parsing:
//
//   - transport.OAuthAuthorizationRequiredError: returned when WithHTTPOAuth is set
//     (carries the OAuthHandler for metadata discovery)
//   - transport.ErrUnauthorized: returned when no OAuth handler is configured
//
// This replaces the previous approach of string-matching "401"/"unauthorized" in error
// messages and parsing WWW-Authenticate headers from error strings.
func CheckForAuthRequiredError(err error, url string) *AuthRequiredError {
	if err == nil {
		return nil
	}

	var oauthErr *transport.OAuthAuthorizationRequiredError
	if errors.As(err, &oauthErr) {
		return &AuthRequiredError{
			URL:      url,
			AuthInfo: AuthInfo{},
			Err:      fmt.Errorf("server returned 401 Unauthorized"),
		}
	}

	if errors.Is(err, transport.ErrUnauthorized) {
		return &AuthRequiredError{
			URL:      url,
			AuthInfo: AuthInfo{},
			Err:      fmt.Errorf("server returned 401 Unauthorized"),
		}
	}

	return nil
}
