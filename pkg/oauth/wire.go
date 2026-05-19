package oauth

// HTTP header names and authentication scheme used by OAuth/OIDC flows.
// These match RFC 6749 / 6750 / 8693 wire format and must not be renamed.
const (
	HeaderAuthorization = "Authorization"
	HeaderWWWAuth       = "WWW-Authenticate"

	SchemeBearer = "Bearer"
)

// Token-endpoint form field names (RFC 6749 §4 + RFC 7636 PKCE + RFC 8693).
const (
	FormFieldGrantType     = "grant_type"
	FormFieldClientID      = "client_id"
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
	GrantTypeTokenExchange     = "urn:ietf:params:oauth:grant-type:token-exchange" //nolint:gosec // G101: RFC 8693 grant-type URN, not a credential
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
