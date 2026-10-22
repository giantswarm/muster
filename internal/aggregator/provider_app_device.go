package aggregator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// statusField is the status member of a tool result's structured content.
const statusField = "status"

// deviceCodeClient is the device-code sign-in of one provider App.
type deviceCodeClient interface {
	RequestCode(ctx context.Context) (*chatgptlogin.Code, error)
	Await(ctx context.Context, code *chatgptlogin.Code) (*pkgoauth.Token, error)
}

// newChatGPTDeviceClient is the device-code sign-in of a chatgptDeviceCode App.
func newChatGPTDeviceClient(app configPkg.ProviderAppConfig) deviceCodeClient {
	return &chatgptlogin.Client{Issuer: app.Issuer, ClientID: app.ClientID, TokenEndpoint: app.TokenEndpoint}
}

// grantDeviceCodeApp answers core_auth_login app=<name> for a device-code App:
// the code to enter and OpenAI's device page, the same code while it is
// outstanding. The grant is filed when the person enters it, as the person's
// (grantScope subject).
func (p *AuthToolProvider) grantDeviceCodeApp(ctx context.Context, sessionID, sub string, app configPkg.ProviderAppConfig, limit attemptLimit) *api.CallToolResult {
	a := p.aggregator
	flow := api.ProviderAppFlowPrefix + app.Name
	if a.deviceSignIns.outstanding(ctx, sub, app.Name) == nil && limit != nil {
		if refused := limit(sub, flow); refused != nil {
			return refused
		}
	}
	if err := a.providerApps.pin(ctx, app.Name); err != nil {
		if a.authMetrics != nil {
			a.authMetrics.RecordLoginFailure(flow, sub, "authorization_server_pin_failed")
		}
		return &api.CallToolResult{Content: []any{fmt.Sprintf("Cannot grant provider App '%s': %v", app.Name, err)}, IsError: true}
	}
	code, err := a.deviceSignIns.start(ctx, sub, app, func(token *pkgoauth.Token) {
		a.fileDeviceCodeGrant(sessionID, sub, app, token)
	})
	if err != nil {
		if a.authMetrics != nil {
			a.authMetrics.RecordLoginFailure(flow, sub, "device_code_request_failed")
		}
		return &api.CallToolResult{
			Content:           []any{fmt.Sprintf("Cannot connect provider App '%s': %v", app.Name, err)},
			IsError:           true,
			StructuredContent: map[string]any{statusField: string(pkgoauth.SessionServerStatusAuthRequired), providerAppArg: app.Name, "error": err.Error()},
		}
	}
	return &api.CallToolResult{
		Content: []any{fmt.Sprintf(
			"Connect provider App '%s'\n\n"+
				"1. Open %s and sign in.\n"+
				"2. Enter the one-time code %s (it expires at %s).\n\n"+
				"Enter it only because you started this connection yourself. auth://status reports %s until the code is entered, then %s.",
			app.Name, code.VerificationURL, code.UserCode, code.ExpiresAt.UTC().Format(time.RFC3339),
			pkgoauth.SessionServerStatusPending, pkgoauth.SessionServerStatusConnected)},
		StructuredContent: map[string]any{
			statusField:       string(pkgoauth.SessionServerStatusPending),
			providerAppArg:    app.Name,
			"userCode":        code.UserCode,
			"verificationUrl": code.VerificationURL,
			"expiresAt":       code.ExpiresAt.UTC().Format(time.RFC3339),
		},
	}
}

// fileDeviceCodeGrant stores the tokens of an entered code as the person's
// grant of the App: under the session that asked and, the App being
// subject-scoped, under the person, where the refresh and the credential
// provider read it.
func (a *AggregatorServer) fileDeviceCodeGrant(sessionID, sub string, app configPkg.ProviderAppConfig, token *pkgoauth.Token) {
	handler := api.GetOAuthHandler()
	if handler == nil || !handler.IsEnabled() {
		a.deviceSignIns.fail(sub, app.Name, errors.New("the OAuth proxy was disabled before the code was entered"))
		return
	}
	if token.Scope == "" {
		token.Scope = app.Scopes
	}
	handler.StoreToken(sessionID, sub, app.Issuer, &api.OAuthToken{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt,
		Scope:        token.Scope,
		IDToken:      token.IDToken,
		Issuer:       app.Issuer,
	})
	if a.authMetrics != nil {
		a.authMetrics.RecordLoginSuccess(api.ProviderAppFlowPrefix+app.Name, sub)
	}
	logging.Audit(logging.AuditEvent{
		Action:  "provider_app_granted",
		Outcome: "success",
		Subject: logging.TruncateIdentifier(sub),
		Target:  app.Name,
		Details: fmt.Sprintf("issuer=%s flow=%s account=%t", app.Issuer, app.Flow, chatgptlogin.AccountID(token.IDToken) != ""),
	})
}

// deviceCodeStatus fills in a device-code App's pending code and last
// failure for a person without its grant. A code no replica polls any more
// is polled here from now on.
func (a *AggregatorServer) deviceCodeStatus(ctx context.Context, sessionID, sub string, app configPkg.ProviderAppConfig, status *pkgoauth.ProviderAppAuthStatus) {
	code := a.deviceSignIns.current(ctx, sub, app, func(token *pkgoauth.Token) {
		a.fileDeviceCodeGrant(sessionID, sub, app, token)
	})
	if code != nil {
		expiresAt := code.ExpiresAt.UTC()
		status.Status = pkgoauth.SessionServerStatusPending
		status.UserCode = code.UserCode
		status.VerificationURL = code.VerificationURL
		status.CodeExpiresAt = &expiresAt
		return
	}
	status.LastFailure = a.deviceSignIns.lastFailure(ctx, sub, status.Name)
}

// pendingDeviceCode returns the structured content of a core_auth_login
// answer that issued a device code, and whether it is one.
func pendingDeviceCode(result *api.CallToolResult) (map[string]any, bool) {
	if result == nil || result.IsError {
		return nil, false
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured[statusField] != string(pkgoauth.SessionServerStatusPending) {
		return nil, false
	}
	return structured, true
}
