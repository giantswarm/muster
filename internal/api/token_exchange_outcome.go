package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// TokenExchangeFailureClass names why a per-user RFC 8693 token exchange
// failed in a way that is the server's, not one caller's. It doubles as the
// reason of the MCPServer's Ready condition while the server is Failed for it.
//
// A failure that is specific to the caller's own token -- invalid_grant, an
// expired subject token, a rejected audience -- has no class: it is reported
// on the session and never changes the server's state.
type TokenExchangeFailureClass string

const (
	// TokenExchangeFailureNone: the failure is the caller's, not the server's.
	TokenExchangeFailureNone TokenExchangeFailureClass = ""

	// TokenExchangeFailureCredentials: the client credentials are wrong or
	// cannot be loaded -- the referenced Secret is missing or lacks the keys,
	// or the authorization server answered invalid_client.
	TokenExchangeFailureCredentials TokenExchangeFailureClass = "TokenExchangeCredentials"

	// TokenExchangeFailureEndpoint: the token endpoint did not answer (DNS,
	// connection refused, TLS, timeout) or answered with a server error.
	TokenExchangeFailureEndpoint TokenExchangeFailureClass = "TokenExchangeEndpoint"

	// TokenExchangeFailureConnector: the authorization server does not know
	// the configured connector.
	TokenExchangeFailureConnector TokenExchangeFailureClass = "TokenExchangeConnector"
)

// TokenExchangeCredentialsError reports that the client credentials for a
// token exchange could not be loaded: the referenced Secret does not exist,
// is not readable or lacks the configured keys. It is typed so the failure is
// classified by what it is, not by the text of a Kubernetes API error.
type TokenExchangeCredentialsError struct {
	// Secret is the "namespace/name" of the referenced Secret.
	Secret string
	Err    error
}

// NewTokenExchangeCredentialsError wraps err as a credentials-loading failure
// for the Secret ref, resolved against defaultNamespace.
func NewTokenExchangeCredentialsError(ref *ClientCredentialsSecretRef, defaultNamespace string, err error) error {
	secret := "<unset>"
	if ref != nil {
		namespace := ref.Namespace
		if namespace == "" {
			namespace = defaultNamespace
		}
		secret = namespace + "/" + ref.Name
	}
	return &TokenExchangeCredentialsError{Secret: secret, Err: err}
}

func (e *TokenExchangeCredentialsError) Error() string {
	return fmt.Sprintf("token exchange credentials from Secret %s: %v", e.Secret, e.Err)
}

func (e *TokenExchangeCredentialsError) Unwrap() error { return e.Err }

// ClassifyTokenExchangeError decides whether a failed token exchange is the
// server's (every caller fails the same way) or the caller's.
//
// The authorization server's answer arrives as text -- the OAuth error code
// and description are formatted into the error by the exchange client -- so
// the codes are matched in it. invalid_client is RFC 6749's answer to wrong
// client credentials; an unknown connector is answered as invalid_request
// with a description naming the connector (Dex: "Requested connector does not
// exist"), and the exchange client's own connector validation names it the
// same way. Anything else the authorization server says about the request is
// about the subject token, which is the caller's.
func ClassifyTokenExchangeError(err error) TokenExchangeFailureClass {
	if err == nil {
		return TokenExchangeFailureNone
	}

	var credentialsErr *TokenExchangeCredentialsError
	if errors.As(err, &credentialsErr) {
		return TokenExchangeFailureCredentials
	}

	var urlErr *url.Error
	var netErr net.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return TokenExchangeFailureEndpoint
	}

	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "invalid_client"):
		return TokenExchangeFailureCredentials
	case strings.Contains(text, "connector"):
		return TokenExchangeFailureConnector
	case isTokenEndpointServerError(text):
		return TokenExchangeFailureEndpoint
	default:
		return TokenExchangeFailureNone
	}
}

// isTokenEndpointServerError reports whether the lower-cased error text names
// an HTTP 5xx answer from the token endpoint, as the exchange client formats a
// non-OAuth error body ("token exchange failed with status 502: ...").
func isTokenEndpointServerError(text string) bool {
	for _, marker := range []string{"with status 50", "with status 51", "status code 50", "status code 51"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// TokenExchangeOutcomeRecorder is implemented by an MCPServer service that is
// served per session through RFC 8693 token exchange. The aggregator reports
// every exchange it performs for a caller through
// ReportMCPServerTokenExchange; the service turns a failure that is the
// server's into its Failed state and a success back out of it.
type TokenExchangeOutcomeRecorder interface {
	// RecordTokenExchangeOutcome records one exchange: err is nil when it
	// succeeded.
	RecordTokenExchangeOutcome(err error)
}

// ReportMCPServerTokenExchange tells the named MCPServer service how a
// per-user token exchange ended (err nil: it succeeded). Best-effort: a server
// the registry does not know, or whose service does not record outcomes, is
// skipped without error, as UpdateMCPServerState does.
func ReportMCPServerTokenExchange(name string, err error) {
	registry := GetServiceRegistry()
	if registry == nil {
		return
	}
	service, exists := registry.Get(name)
	if !exists {
		logging.Debug("API", "Cannot record token exchange outcome for MCPServer %s: not found in registry", name)
		return
	}
	recorder, ok := service.(TokenExchangeOutcomeRecorder)
	if !ok {
		return
	}
	recorder.RecordTokenExchangeOutcome(err)
}
