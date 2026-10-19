package oauth

// HTTP header names and authentication scheme used by OAuth/OIDC flows.
// These match RFC 6749 / 6750 / 8693 wire format and must not be renamed.
const (
	HeaderAuthorization = "Authorization"
	HeaderWWWAuth       = "WWW-Authenticate"

	SchemeBearer = "Bearer"
)

// HeaderMusterIDToken carries the session's upstream ID token to an MCPServer
// with spec.auth.forwardIdentity, next to the pinned grant in Authorization.
// Servers read it by this name: it must not be renamed.
const HeaderMusterIDToken = "X-Muster-Id-Token" //nolint:gosec // G101: a header name, not a credential

// HeaderProviderAppGrantPrefix starts the header that carries the person's
// access token of one provider App to an MCPServer with spec.auth.providerApps:
// X-Muster-Grant-<app>, one header per App the person granted. Servers read it
// by this name: it must not be renamed.
const HeaderProviderAppGrantPrefix = "X-Muster-Grant-" //nolint:gosec // G101: a header name, not a credential

// ProviderAppGrantHeader is the header carrying the person's token of the
// provider App app.
func ProviderAppGrantHeader(app string) string {
	return HeaderProviderAppGrantPrefix + app
}

// A tool result whose structuredContent is an object with "status":
// StatusGrantRequired and "apps": [<app>, ...] says the call needs the
// person's grant of those provider Apps. muster answers it auth_required with
// each App's sign-in link.
const (
	StatusGrantRequired = "grant_required"
	FieldGrantApps      = "apps"
)

// Token-endpoint form field names (RFC 6749 §4 + RFC 7636 PKCE + RFC 8693).
const (
	FormFieldGrantType     = "grant_type"
	FormFieldClientID      = "client_id"
	FormFieldClientSecret  = "client_secret"
	FormFieldCode          = "code"
	FormFieldCodeVerifier  = "code_verifier"
	FormFieldRefreshToken  = "refresh_token"
	FormFieldRedirectURI   = "redirect_uri"
	FormFieldScope         = "scope"
	FormFieldSubjectToken  = "subject_token"
	FormFieldRequestedAud  = "audience"
	FormFieldSubjectTokenT = "subject_token_type"
)

// Grant types accepted at the token endpoint.
const (
	GrantTypeAuthorizationCode = "authorization_code"
	GrantTypeRefreshToken      = "refresh_token"
	GrantTypeTokenExchange     = "urn:ietf:params:oauth:grant-type:token-exchange" //nolint:gosec // G101: RFC 8693 grant-type URN, a protocol constant, not a credential
)

// Authorization-request PKCE challenge methods (RFC 7636).
const (
	CodeChallengeMethodS256  = "S256"
	CodeChallengeMethodPlain = "plain"
)

// JSON field names returned in token-endpoint and error responses.
const (
	JSONFieldAccessToken      = "access_token"
	JSONFieldRefreshToken     = "refresh_token"
	JSONFieldIDToken          = "id_token"
	JSONFieldTokenType        = "token_type"
	JSONFieldExpiresIn        = "expires_in"
	JSONFieldError            = "error"
	JSONFieldErrorDescription = "error_description"
)

// OAuth 2.0 error codes (RFC 6749 §5.2, §4.1.2.1).
const (
	ErrInvalidRequest       = "invalid_request"
	ErrInvalidClient        = "invalid_client"
	ErrInvalidGrant         = "invalid_grant"
	ErrUnauthorizedClient   = "unauthorized_client"
	ErrUnsupportedGrantType = "unsupported_grant_type"
	ErrInvalidScope         = "invalid_scope"
	ErrAccessDenied         = "access_denied"
	ErrServerError          = "server_error"
	ErrInvalidToken         = "invalid_token"
)
