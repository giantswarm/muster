package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/mark3labs/mcp-go/client/transport"
)

const (
	// clientName is how muster identifies itself to a remote MCP server in the
	// initialize handshake, and the identity sigV4RoleSessionName tracks so
	// CloudTrail separates muster from other callers of the same role. The stdio
	// client announces "muster" instead, which predates this constant.
	clientName = "muster-aggregator"

	// clientVersion is the version every MCP client in this package reports in
	// the initialize handshake's clientInfo.
	//
	// A fixed string, not the build version: pkg/project.Version() holds that,
	// and reporting it here would change what every backend records. Worth
	// revisiting as its own change — a backend's logs currently cannot tell one
	// muster build from another.
	clientVersion = "1.0.0"
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

	// Resource is the canonical URI the server declares for itself in its
	// RFC 9728 metadata. It is the RFC 8707 `resource` value muster sends on
	// authorization and token requests for this server.
	Resource string
}

// AuthRequiredError is returned when an MCP server requires OAuth authentication
// before the protocol handshake can complete. This error contains the information
// needed to initiate the OAuth flow.
type AuthRequiredError struct {
	// URL is the endpoint that returned the 401
	URL string

	// AuthInfo contains the OAuth parameters extracted from the 401 response
	AuthInfo AuthInfo

	// Challenge is the backend's WWW-Authenticate challenge from the 401 that
	// produced this error, when the client's transport recorded one (see
	// challengeRecorder). Its error and error_description parameters say why
	// the credential was refused; the request and the token are never kept.
	// nil when the 401 carried no parseable challenge or the transport does
	// not record them.
	Challenge *pkgoauth.AuthChallenge

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

// AuthRequired is a marker method that satisfies api.authRequiredError,
// enabling detection via api.IsAuthRequiredError without direct imports.
func (e *AuthRequiredError) AuthRequired() bool {
	return true
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

// InitializeRefusedError is returned when the endpoint answered the initialize
// POST with a status that is up and routing -- it is not a connection failure
// -- but that does not let the handshake complete right now. Two kinds, told
// apart by the status so the log and the CR status name the real cause and the
// caller acts on it:
//
//   - The path does not serve the MCP protocol (yet): a backend whose route is
//     being rolled out answers 404 until the new pod takes it over, a server
//     that speaks only the legacy SSE transport answers 405. Corrected outside
//     muster, so the caller retries with muster's backoff instead of settling
//     the server in Failed (issue #1295).
//   - The endpoint is temporarily refusing the load: 429 Too Many Requests or
//     503 Service Unavailable, a transient refusal with a Retry-After, never a
//     transport mismatch. The session's connection is kept and retried after
//     the Retry-After the server asked for, not dropped (issue #1303).
//
// mcp-go reduces a 4xx on the initialize POST to transport.ErrLegacySSEServer
// and a 503 to a status-in-text error, both without the Retry-After; the
// client's transport records the status and the Retry-After header (see
// challengeRecorder) so they reach the log and the CR status.
type InitializeRefusedError struct {
	// URL is the endpoint that refused the initialize.
	URL string

	// StatusCode is the HTTP status the endpoint answered with; 0 when the
	// transport did not record one.
	StatusCode int

	// RetryAfter is the delay the endpoint asked to be retried after (the
	// Retry-After header of a 429 or 503); 0 when none was sent.
	RetryAfter time.Duration

	// Err is the underlying error mcp-go returned (transport.ErrLegacySSEServer
	// for a 4xx, a status-in-text error for a 503).
	Err error
}

// Error implements the error interface. It names the status and, for a 429 or
// 503, the Retry-After. "likely a legacy SSE server" is said only for the 405
// that actually means it, never for a status that has its own meaning (issue
// #1303).
func (e *InitializeRefusedError) Error() string {
	if e.StatusCode == 0 {
		return "endpoint refused the initialize POST with a 4xx"
	}
	msg := fmt.Sprintf("endpoint answered the initialize POST with HTTP %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if e.StatusCode == http.StatusMethodNotAllowed {
		msg += ", likely a legacy SSE server"
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf("; retry after %s", e.RetryAfter)
	}
	return msg
}

// Unwrap returns the underlying error.
func (e *InitializeRefusedError) Unwrap() error {
	return e.Err
}

// CheckForAuthRequiredError examines an error to determine if it's a 401 authentication
// required error. It uses mcp-go's typed error detection instead of string parsing:
//
//   - transport.OAuthAuthorizationRequiredError: returned when WithHTTPOAuth is set.
//     The error carries an OAuthHandler that can discover server metadata (issuer, scopes).
//   - transport.ErrUnauthorized ("unauthorized (401)"): returned by some paths when
//     no OAuth handler is configured.
//   - transport.ErrAuthorizationRequired ("authorization required"): introduced in
//     mcp-go v0.49.0 and returned by the streamable-http transport for 401 responses
//     when the client has no valid token. Muster must treat this identically to the
//     other two so that auth-required servers get registered in pending-auth state
//     instead of failing outright.
func CheckForAuthRequiredError(ctx context.Context, err error, url string) *AuthRequiredError {
	if err == nil {
		return nil
	}

	// The wrapped Err keeps transport.ErrUnauthorized in the chain so callers
	// that test with pkgoauth.IsOAuthUnauthorizedError / errors.Is still
	// recognize an AuthRequiredError as a 401 — core_auth_login relies on this
	// to clear a stored-but-rejected token and issue a fresh auth challenge
	// instead of dead-ending with "try again".
	var oauthErr *transport.OAuthAuthorizationRequiredError
	if errors.As(err, &oauthErr) {
		authInfo := extractAuthInfoFromHandler(ctx, oauthErr.Handler)
		return &AuthRequiredError{
			URL:      url,
			AuthInfo: authInfo,
			Err:      fmt.Errorf("server returned 401 Unauthorized: %w", transport.ErrUnauthorized),
		}
	}

	if errors.Is(err, transport.ErrUnauthorized) ||
		errors.Is(err, transport.ErrAuthorizationRequired) ||
		errors.Is(err, transport.ErrOAuthAuthorizationRequired) {
		return &AuthRequiredError{
			URL:      url,
			AuthInfo: AuthInfo{},
			Err:      fmt.Errorf("server returned 401 Unauthorized: %w", transport.ErrUnauthorized),
		}
	}

	return nil
}

// extractAuthInfoFromHandler attempts to extract OAuth metadata from a mcp-go OAuthHandler.
// It calls GetServerMetadata which may discover the authorization server via the
// MCP server's .well-known/oauth-protected-resource endpoint (RFC 9728).
// Returns an empty AuthInfo if metadata discovery fails.
func extractAuthInfoFromHandler(ctx context.Context, handler *transport.OAuthHandler) AuthInfo {
	if handler == nil {
		return AuthInfo{}
	}

	metadata, err := handler.GetServerMetadata(ctx)
	if err != nil {
		logging.Debug("AuthRequiredError", "Failed to get server metadata from OAuthHandler: %v", err)
		return AuthInfo{}
	}

	if metadata == nil {
		return AuthInfo{}
	}

	return AuthInfo{
		Issuer: metadata.Issuer,
		Scope:  strings.Join(metadata.ScopesSupported, " "),
	}
}
