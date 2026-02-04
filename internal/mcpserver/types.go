package mcpserver

import (
	"fmt"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
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

	// AuthChallenge contains the parsed WWW-Authenticate header information
	// from pkg/oauth for structured auth challenge handling
	AuthChallenge *pkgoauth.AuthChallenge

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
	// Check AuthChallenge first (preferred)
	if e.AuthChallenge != nil && e.AuthChallenge.IsOAuthChallenge() {
		return true
	}
	// Fallback to AuthInfo
	return e.AuthInfo.Issuer != "" || e.AuthInfo.ResourceMetadataURL != ""
}

// GetIssuer returns the OAuth issuer URL from the error.
// It prefers AuthChallenge if available, falls back to AuthInfo.
func (e *AuthRequiredError) GetIssuer() string {
	if e == nil {
		return ""
	}
	if e.AuthChallenge != nil {
		issuer := e.AuthChallenge.GetIssuer()
		if issuer != "" {
			return issuer
		}
	}
	return e.AuthInfo.Issuer
}

// GetScope returns the OAuth scope from the error.
func (e *AuthRequiredError) GetScope() string {
	if e == nil {
		return ""
	}
	if e.AuthChallenge != nil && e.AuthChallenge.Scope != "" {
		return e.AuthChallenge.Scope
	}
	return e.AuthInfo.Scope
}

// GetResourceMetadataURL returns the resource metadata URL from the error.
func (e *AuthRequiredError) GetResourceMetadataURL() string {
	if e == nil {
		return ""
	}
	if e.AuthChallenge != nil && e.AuthChallenge.ResourceMetadataURL != "" {
		return e.AuthChallenge.ResourceMetadataURL
	}
	return e.AuthInfo.ResourceMetadataURL
}

// CheckForAuthRequiredError examines an error to determine if it's a 401 authentication
// required error. If so, it returns an AuthRequiredError with parsed OAuth parameters.
// This is a shared helper used by SSEClient and StreamableHTTPClient.
//
// The function uses the pkg/oauth utilities for structured 401 detection,
// leveraging ParseWWWAuthenticateFromError for proper header parsing.
func CheckForAuthRequiredError(err error, url string) *AuthRequiredError {
	if err == nil {
		return nil
	}

	// Use the shared pkg/oauth utility to check if this is a 401 error
	if !pkgoauth.Is401Error(err) {
		return nil
	}

	// Parse auth challenge from the error using pkg/oauth
	challenge := pkgoauth.ParseWWWAuthenticateFromError(err)

	// Build AuthInfo from the parsed challenge
	authInfo := AuthInfo{}
	if challenge != nil {
		authInfo.Issuer = challenge.GetIssuer()
		authInfo.Scope = challenge.Scope
		authInfo.ResourceMetadataURL = challenge.ResourceMetadataURL
	}

	return &AuthRequiredError{
		URL:           url,
		AuthInfo:      authInfo,
		AuthChallenge: challenge,
		Err:           fmt.Errorf("server returned 401 Unauthorized"),
	}
}
