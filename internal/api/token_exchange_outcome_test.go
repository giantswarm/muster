package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

// TestClassifyTokenExchangeError pins which exchange failures are the
// server's -- every caller fails the same way, so the server's state follows
// -- and which are one caller's own and leave the server alone.
func TestClassifyTokenExchangeError(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want TokenExchangeFailureClass
	}{
		"no error": {err: nil, want: TokenExchangeFailureNone},
		"credentials Secret missing": {
			err: fmt.Errorf("failed to load client credentials: %w", NewTokenExchangeCredentialsError(
				&ClientCredentialsSecretRef{Name: "remote-token-exchange-credentials"}, "agent-platform",
				errors.New(`secrets "remote-token-exchange-credentials" not found`))),
			want: TokenExchangeFailureCredentials,
		},
		"client rejected": {
			err:  fmt.Errorf("token exchange failed for remote-mcp: %w", errors.New("token exchange failed: invalid_client - Invalid client credentials.")),
			want: TokenExchangeFailureCredentials,
		},
		"connector unknown to the authorization server": {
			err:  errors.New("token exchange failed: invalid_request - Requested connector does not exist."),
			want: TokenExchangeFailureConnector,
		},
		"connector rejected by the exchange client": {
			err:  errors.New("invalid connector ID: must match ^[a-zA-Z0-9_-]+$"),
			want: TokenExchangeFailureConnector,
		},
		"token endpoint not answering": {
			err:  fmt.Errorf("token exchange failed: %w", &url.Error{Op: "Post", URL: "https://dex.example.test/token", Err: errors.New("dial tcp: connection refused")}),
			want: TokenExchangeFailureEndpoint,
		},
		"token endpoint network error": {
			err:  fmt.Errorf("token exchange request failed: %w", &net.OpError{Op: "dial", Err: errors.New("no route to host")}),
			want: TokenExchangeFailureEndpoint,
		},
		"token endpoint timed out": {
			err:  fmt.Errorf("token exchange failed: %w", context.DeadlineExceeded),
			want: TokenExchangeFailureEndpoint,
		},
		"token endpoint answered 5xx": {
			err:  errors.New("token exchange failed with status 502: <html>Bad Gateway</html>"),
			want: TokenExchangeFailureEndpoint,
		},
		"subject token rejected": {
			err:  errors.New("token exchange failed: invalid_grant - subject token is expired"),
			want: TokenExchangeFailureNone,
		},
		"subject token not trusted": {
			err:  errors.New("token exchange failed: invalid_request - Unable to verify subject token."),
			want: TokenExchangeFailureNone,
		},
		"issuer of the exchanged token": {
			err:  errors.New("issuer validation failed: token issuer mismatch"),
			want: TokenExchangeFailureNone,
		},
		"caller has no ID token": {
			err:  errors.New("no ID token available for token exchange"),
			want: TokenExchangeFailureNone,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ClassifyTokenExchangeError(tc.err); got != tc.want {
				t.Errorf("ClassifyTokenExchangeError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestTokenExchangeCredentialsError names the Secret an operator has to look
// at, resolved against the server's namespace, and unwraps to the cause.
func TestTokenExchangeCredentialsError(t *testing.T) {
	cause := errors.New(`secrets "remote-token-exchange-credentials" not found`)
	err := NewTokenExchangeCredentialsError(&ClientCredentialsSecretRef{Name: "remote-token-exchange-credentials"}, "agent-platform", cause)

	want := `token exchange credentials from Secret agent-platform/remote-token-exchange-credentials: secrets "remote-token-exchange-credentials" not found`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, cause) {
		t.Error("the cause must stay reachable through errors.Is")
	}

	explicit := NewTokenExchangeCredentialsError(&ClientCredentialsSecretRef{Name: "creds", Namespace: "other"}, "agent-platform", cause)
	if got := explicit.(*TokenExchangeCredentialsError).Secret; got != "other/creds" {
		t.Errorf("an explicit namespace wins over the default, got %q", got)
	}
	if got := NewTokenExchangeCredentialsError(nil, "agent-platform", cause).(*TokenExchangeCredentialsError).Secret; got != "<unset>" {
		t.Errorf("a nil reference reads <unset>, got %q", got)
	}
}

// recordingMockService is a registry entry that records the token exchange
// outcomes reported to it, the way an MCPServer service does.
type recordingMockService struct {
	mockServiceInfo
	outcomes []error
}

func (m *recordingMockService) RecordTokenExchangeOutcome(err error) {
	m.outcomes = append(m.outcomes, err)
}

// TestReportMCPServerTokenExchange hands the outcome to the service and is
// silent about everything it cannot reach: no registry, an unknown server, a
// service that does not record outcomes.
func TestReportMCPServerTokenExchange(t *testing.T) {
	RegisterServiceRegistry(nil)
	ReportMCPServerTokenExchange("svc", errors.New("no registry, no panic"))

	registry := newMockServiceRegistryHandler()
	recorder := &recordingMockService{mockServiceInfo: mockServiceInfo{name: "svc", svcType: TypeMCPServer, state: StateAwaitingSession}}
	registry.addService(recorder)
	registry.addService(&mockServiceInfo{name: "plain", svcType: TypeMCPServer, state: StateConnected})
	RegisterServiceRegistry(registry)
	t.Cleanup(func() { RegisterServiceRegistry(nil) })

	failure := errors.New("token exchange failed: invalid_client")
	ReportMCPServerTokenExchange("svc", failure)
	ReportMCPServerTokenExchange("svc", nil)
	ReportMCPServerTokenExchange("plain", failure)
	ReportMCPServerTokenExchange("unknown", failure)

	if len(recorder.outcomes) != 2 || !errors.Is(recorder.outcomes[0], failure) || recorder.outcomes[1] != nil {
		t.Errorf("the service must receive the failure and then the success, got %v", recorder.outcomes)
	}
}
