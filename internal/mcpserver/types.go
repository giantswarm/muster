package mcpserver

import (
	"fmt"
	"net/http"
	"strings"

	"muster/internal/oauth"
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

// CheckForAuthRequiredError examines an error to determine if it's a 401 authentication
// required error. If so, it returns an AuthRequiredError with parsed OAuth parameters.
// This is a shared helper used by SSEClient and StreamableHTTPClient.
func CheckForAuthRequiredError(err error, url string) *AuthRequiredError {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check for 401 status code in the error message
	// The mcp-go library returns errors like "request failed with status 401: ..."
	if !strings.Contains(errStr, "401") &&
		!strings.Contains(errStr, http.StatusText(http.StatusUnauthorized)) {
		return nil
	}

	// Extract WWW-Authenticate header information if available
	authInfo := AuthInfo{}

	// Try to parse any WWW-Authenticate-style information from the error
	if strings.Contains(errStr, "Bearer") {
		authInfo = ParseAuthInfoFromError(errStr)
	}

	return &AuthRequiredError{
		URL:      url,
		AuthInfo: authInfo,
		Err:      fmt.Errorf("server returned 401 Unauthorized"),
	}
}

// ParseAuthInfoFromError attempts to extract OAuth information from an error message.
// This is a best-effort parse since we can't directly access HTTP response headers.
func ParseAuthInfoFromError(errStr string) AuthInfo {
	info := AuthInfo{}

	// Try to parse as WWW-Authenticate header format if present
	if idx := strings.Index(errStr, "Bearer"); idx >= 0 {
		headerPart := errStr[idx:]
		// Find the end of the Bearer challenge
		if endIdx := strings.Index(headerPart, "\n"); endIdx > 0 {
			headerPart = headerPart[:endIdx]
		}
		params := oauth.ParseWWWAuthenticate(headerPart)
		if params != nil {
			info.Issuer = params.Realm
			info.Scope = params.Scope
			info.ResourceMetadataURL = params.ResourceMetadataURL
		}
	}

	return info
}
