package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

// deviceSignInKey is one person's sign-in to one App.
type deviceSignInKey struct{ sub, app string }

// deviceSignIn is an outstanding code and the poll that waits for it.
type deviceSignIn struct {
	code   *chatgptlogin.Code
	cancel context.CancelFunc
}

// deviceSignIns are the device-code sign-ins a replica polls: at most one
// outstanding code per person and App, and the last failure of each. The poll
// runs on the replica that issued the code and files the grant in the shared
// token store, where every replica finds it.
type deviceSignIns struct {
	newClient func(configPkg.ProviderAppConfig) deviceCodeClient

	mu       sync.Mutex
	ctx      context.Context
	stop     context.CancelFunc
	pending  map[deviceSignInKey]*deviceSignIn
	failures map[deviceSignInKey]string
	polls    sync.WaitGroup
}

func newDeviceSignIns() *deviceSignIns {
	ctx, stop := context.WithCancel(context.Background())
	return &deviceSignIns{
		newClient: newChatGPTDeviceClient,
		ctx:       ctx,
		stop:      stop,
		pending:   make(map[deviceSignInKey]*deviceSignIn),
		failures:  make(map[deviceSignInKey]string),
	}
}

// outstanding returns the person's outstanding code for the App, nil when
// none is.
func (d *deviceSignIns) outstanding(sub, app string) *chatgptlogin.Code {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if signIn := d.pending[deviceSignInKey{sub, app}]; signIn != nil {
		return signIn.code
	}
	return nil
}

// lastFailure returns why the person's last sign-in to the App failed.
func (d *deviceSignIns) lastFailure(sub, app string) string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.failures[deviceSignInKey{sub, app}]
}

// fail records a sign-in's failure.
func (d *deviceSignIns) fail(sub, app string, err error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failures[deviceSignInKey{sub, app}] = err.Error()
}

// start requests a code and polls for it in the background; complete files
// the resulting grant. An outstanding code is returned instead of a new one.
func (d *deviceSignIns) start(ctx context.Context, sub string, app configPkg.ProviderAppConfig, complete func(*pkgoauth.Token)) (*chatgptlogin.Code, error) {
	if d == nil {
		return nil, errors.New("device-code sign-in is not available")
	}
	key := deviceSignInKey{sub, app.Name}
	if code := d.outstanding(sub, app.Name); code != nil {
		return code, nil
	}
	client := d.newClient(app)
	code, err := client.RequestCode(ctx)
	if err != nil {
		d.fail(sub, app.Name, err)
		return nil, err
	}

	d.mu.Lock()
	if current := d.pending[key]; current != nil {
		// A concurrent call issued a code first: that one is the person's.
		d.mu.Unlock()
		return current.code, nil
	}
	if d.ctx.Err() != nil {
		d.mu.Unlock()
		return nil, errors.New("muster is shutting down")
	}
	pollCtx, cancel := context.WithDeadline(d.ctx, code.ExpiresAt.Add(time.Minute))
	signIn := &deviceSignIn{code: code, cancel: cancel}
	d.pending[key] = signIn
	delete(d.failures, key)
	d.polls.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.polls.Done()
		defer cancel()
		token, err := client.Await(pollCtx, code)
		d.mu.Lock()
		current := d.pending[key] == signIn
		if current {
			delete(d.pending, key)
		}
		d.mu.Unlock()
		if !current {
			return // cancelled by a disconnect
		}
		if err != nil {
			if pollCtx.Err() != nil && d.ctx.Err() != nil {
				return // muster stopped
			}
			if errors.Is(err, context.DeadlineExceeded) {
				err = chatgptlogin.ErrExpired
			}
			d.fail(sub, app.Name, err)
			logging.WarnWithAttrs("Aggregator", "provider_app_device_sign_in_failed",
				slog.String("app", app.Name),
				slog.String("subject", logging.TruncateIdentifier(sub)),
				slog.String("error", err.Error()))
			return
		}
		complete(token)
	}()
	return code, nil
}

// cancel ends the person's outstanding sign-in to the App and forgets its
// failure.
func (d *deviceSignIns) cancel(sub, app string) {
	if d == nil {
		return
	}
	key := deviceSignInKey{sub, app}
	d.mu.Lock()
	defer d.mu.Unlock()
	if signIn := d.pending[key]; signIn != nil {
		signIn.cancel()
		delete(d.pending, key)
	}
	delete(d.failures, key)
}

// shutdown cancels every poll and waits for them to end.
func (d *deviceSignIns) shutdown() {
	if d == nil {
		return
	}
	d.stop()
	d.polls.Wait()
}

// grantDeviceCodeApp answers core_auth_login app=<name> for a device-code App:
// the code to enter and OpenAI's device page, the same code while it is
// outstanding. The grant is filed when the person enters it, as the person's
// (grantScope subject).
func (p *AuthToolProvider) grantDeviceCodeApp(ctx context.Context, sessionID, sub string, app configPkg.ProviderAppConfig, limit attemptLimit) *api.CallToolResult {
	a := p.aggregator
	flow := api.ProviderAppFlowPrefix + app.Name
	if a.deviceSignIns.outstanding(sub, app.Name) == nil && limit != nil {
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
// failure for a person without its grant.
func (a *AggregatorServer) deviceCodeStatus(sub string, status *pkgoauth.ProviderAppAuthStatus) {
	if code := a.deviceSignIns.outstanding(sub, status.Name); code != nil {
		expiresAt := code.ExpiresAt.UTC()
		status.Status = pkgoauth.SessionServerStatusPending
		status.UserCode = code.UserCode
		status.VerificationURL = code.VerificationURL
		status.CodeExpiresAt = &expiresAt
		return
	}
	status.LastFailure = a.deviceSignIns.lastFailure(sub, status.Name)
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
