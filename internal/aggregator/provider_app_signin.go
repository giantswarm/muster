package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// providerAppArg is the argument of core_auth_login and core_auth_logout that
// names a provider App instead of a server.
const providerAppArg = "app"

// providerAppArgument reads the app argument: isApp when it is set, and the
// refusal of a call that sets both app and server, or app to a non-string.
func providerAppArgument(args map[string]any) (name string, isApp bool, errResult *api.CallToolResult) {
	raw, set := args[providerAppArg]
	if !set || raw == nil {
		return "", false, nil
	}
	name, ok := raw.(string)
	if !ok || name == "" {
		return "", false, &api.CallToolResult{Content: []any{"Error: 'app' must be a non-empty string"}, IsError: true}
	}
	if server, _ := args[resourceServerArg].(string); server != "" {
		return "", false, &api.CallToolResult{Content: []any{"Error: set either 'server' or 'app', not both"}, IsError: true}
	}
	return name, true, nil
}

// unknownProviderApp is the answer for an App muster does not keep.
func (a *AggregatorServer) unknownProviderApp(name string) *api.CallToolResult {
	known := "none is configured"
	if names := a.providerApps.names(); len(names) > 0 {
		known = "configured: " + strings.Join(names, ", ")
	}
	return &api.CallToolResult{
		Content: []any{fmt.Sprintf("Provider App '%s' not found (%s).", name, known)},
		IsError: true,
	}
}

// grantProviderApp answers core_auth_login app=<name>: the person already holds
// the App's grant, or the sign-in link that starts it. The grant is the
// person's (grantScope subject): one sign-in serves every session and every
// server that receives the App's grant. limit is charged only for a new
// sign-in.
func (p *AuthToolProvider) grantProviderApp(ctx context.Context, sessionID, sub, name string, limit attemptLimit) *api.CallToolResult {
	a := p.aggregator
	app, ok := a.providerApps.get(name)
	if !ok {
		return a.unknownProviderApp(name)
	}
	flow := api.ProviderAppFlowPrefix + name
	if a.authMetrics != nil {
		a.authMetrics.RecordLoginAttempt(flow, sub)
	}
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("OAuth is not configured: provider App '%s' cannot be granted without the OAuth proxy.", name)},
			IsError: true,
		}
	}
	if token := api.FullTokenByIssuerForUser(oauthHandler, sessionID, sub, app.Issuer); token != nil && token.AccessToken != "" {
		return &api.CallToolResult{
			Content:           []any{fmt.Sprintf("Provider App '%s' is %s: muster holds your grant.", name, api.AuthMsgAlreadyAuthenticated)},
			StructuredContent: map[string]any{statusField: string(pkgoauth.SessionServerStatusConnected), providerAppArg: name},
		}
	}
	if app.Flow == configPkg.ProviderAppFlowChatGPTDeviceCode {
		return p.grantDeviceCodeApp(ctx, sessionID, sub, app, limit)
	}
	if limit != nil {
		if refused := limit(sub, flow); refused != nil {
			return refused
		}
	}
	// Re-pinned before every sign-in: a rotated client Secret is picked up
	// without a restart.
	if err := a.providerApps.pin(ctx, name); err != nil {
		if a.authMetrics != nil {
			a.authMetrics.RecordLoginFailure(flow, sub, "authorization_server_pin_failed")
		}
		return &api.CallToolResult{Content: []any{fmt.Sprintf("Cannot grant provider App '%s': %v", name, err)}, IsError: true}
	}
	challenge, err := oauthHandler.CreateAuthChallenge(ctx, api.AuthChallengeParams{
		SessionID:  sessionID,
		UserID:     sub,
		ServerName: flow,
		Issuer:     app.Issuer,
		Scope:      app.Scopes,
	})
	if err != nil {
		if a.authMetrics != nil {
			a.authMetrics.RecordLoginFailure(flow, sub, "challenge_creation_failed")
		}
		return &api.CallToolResult{Content: []any{fmt.Sprintf("Failed to create the sign-in for provider App '%s': %v", name, err)}, IsError: true}
	}
	result := authChallengeResult(name, challenge)
	if structured, ok := result.StructuredContent.(map[string]any); ok {
		structured[providerAppArg] = name
	}
	return result
}

// disconnectProviderApp answers core_auth_logout app=<name>: the person's grant
// of the App is revoked at its authorization server and dropped, and no other
// grant is touched. Every server that verifies the App's tokens through an
// MCPServer pin of the same issuer is disconnected for the person's sessions.
func (p *AuthToolProvider) disconnectProviderApp(ctx context.Context, sessionID, sub, name string) *api.CallToolResult {
	a := p.aggregator
	app, ok := a.providerApps.get(name)
	if !ok {
		return a.unknownProviderApp(name)
	}
	flow := api.ProviderAppFlowPrefix + name
	if a.authMetrics != nil {
		a.authMetrics.RecordLogoutAttempt(flow, sub)
	}
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return &api.CallToolResult{
			Content: []any{fmt.Sprintf("OAuth is not configured: provider App '%s' holds no grant.", name)},
			IsError: true,
		}
	}
	a.deviceSignIns.cancel(sub, name)
	held := api.FullTokenByIssuerForUser(oauthHandler, sessionID, sub, app.Issuer) != nil
	api.ClearTokenByIssuerForUser(oauthHandler, sessionID, sub, app.Issuer)
	siblings := a.disconnectIssuerForSubject(ctx, sessionID, sub, "", app.Issuer)
	if a.authMetrics != nil {
		a.authMetrics.RecordLogoutSuccess(flow, sub)
	}
	logging.Audit(logging.AuditEvent{
		Action:  "provider_app_disconnect",
		Outcome: "success",
		Subject: logging.TruncateIdentifier(sub),
		Target:  name,
		Details: fmt.Sprintf("issuer=%s held=%t", app.Issuer, held),
	})

	// A device-code App's provider publishes no revocation endpoint for its
	// client: the grant is dropped in muster only.
	revoked := held && app.Flow != configPkg.ProviderAppFlowChatGPTDeviceCode
	var b strings.Builder
	switch {
	case revoked:
		fmt.Fprintf(&b, "Disconnected provider App '%s': muster dropped your grant and revoked it at the provider, where the provider offers revocation.", name)
	case held:
		fmt.Fprintf(&b, "Disconnected provider App '%s': muster dropped your grant. The provider offers no revocation for it; no Session gets it from muster any more.", name)
	default:
		fmt.Fprintf(&b, "Provider App '%s' held no grant of yours.", name)
	}
	if len(siblings) > 0 {
		fmt.Fprintf(&b, "\n\nAlso disconnected, as they verify the same grant: %s.", strings.Join(siblings, ", "))
	}
	return &api.CallToolResult{
		Content:           []any{b.String()},
		StructuredContent: map[string]any{statusField: "disconnected", providerAppArg: name, "revoked": revoked, "dropped": held},
	}
}

// answerGrantRequired answers a tool result in which a server with
// spec.auth.providerApps names Apps the call needs and the person did not
// grant ({statusField: "grant_required", "apps": [...]}): auth_required with each
// App's sign-in link. Every other result is returned unchanged, so a call that
// needs no missing grant is never refused.
func (a *AggregatorServer) answerGrantRequired(ctx context.Context, serverName, sessionID, sub string, result *mcp.CallToolResult) *mcp.CallToolResult {
	apps := grantRequiredApps(result)
	if len(apps) == 0 {
		return result
	}
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok || !info.AuthConfig.ReceivesProviderApps() {
		return result
	}
	provider := NewAuthToolProvider(a)
	var missing []map[string]any
	var lines []string
	charged := false
	for _, name := range apps {
		if !slices.Contains(info.AuthConfig.ProviderApps, name) {
			continue
		}
		var limit attemptLimit
		if !charged {
			// One attempt for the answer, however many links it issues.
			limit, charged = provider.rateLimited, true
		}
		login := provider.grantProviderApp(ctx, sessionID, sub, name, limit)
		url := challengeURL(login)
		code, pending := pendingDeviceCode(login)
		if url == "" && !pending && !login.IsError {
			continue // granted meanwhile
		}
		entry := map[string]any{providerAppArg: name}
		if pending {
			entry["userCode"], entry["verificationUrl"] = code["userCode"], code["verificationUrl"]
			lines = append(lines, fmt.Sprintf("- %s: open %v and enter the code %v", name, code["verificationUrl"], code["userCode"]))
		} else if url != "" {
			entry["authUrl"] = url
			lines = append(lines, fmt.Sprintf("- %s: %s", name, url))
		} else {
			entry["error"] = contentText(login)
			lines = append(lines, fmt.Sprintf("- %s: %s", name, contentText(login)))
		}
		missing = append(missing, entry)
	}
	if len(missing) == 0 {
		return result
	}
	logging.InfoWithAttrs("Aggregator", "provider_app_grant_required",
		slog.String("server", serverName),
		slog.String("subject", logging.TruncateIdentifier(sub)),
		slog.Int("apps", len(missing)))

	structured := map[string]any{
		statusField:             string(pkgoauth.SessionServerStatusAuthRequired),
		"server":                serverName,
		pkgoauth.FieldGrantApps: missing,
	}
	if url, ok := missing[0]["authUrl"].(string); ok {
		structured["authUrl"] = url
	}
	text := fmt.Sprintf("auth_required: server '%s' needs your grant of a provider App for this call. Sign in once per App, then call again:\n%s",
		serverName, strings.Join(lines, "\n"))
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(text)},
		IsError:           true,
		StructuredContent: structured,
	}
}

// grantRequiredApps returns the Apps a grant_required tool result names.
func grantRequiredApps(result *mcp.CallToolResult) []string {
	if result == nil {
		return nil
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured[statusField] != pkgoauth.StatusGrantRequired {
		return nil
	}
	raw, _ := structured[pkgoauth.FieldGrantApps].([]any)
	var apps []string
	for _, v := range raw {
		if name, ok := v.(string); ok && name != "" && !slices.Contains(apps, name) {
			apps = append(apps, name)
		}
	}
	return apps
}

// providerAppStatuses is the person's grant of every provider App, for the
// auth status; with withURLs the sign-in link of each App awaiting it.
func (p *AuthToolProvider) providerAppStatuses(ctx context.Context, sessionID, sub string, withURLs bool) []pkgoauth.ProviderAppAuthStatus {
	a := p.aggregator
	names := a.providerApps.names()
	if len(names) == 0 || sessionID == "" || sub == "" {
		return nil
	}
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil
	}
	statuses := make([]pkgoauth.ProviderAppAuthStatus, 0, len(names))
	for _, name := range names {
		app, _ := a.providerApps.get(name)
		status := pkgoauth.ProviderAppAuthStatus{Name: name, Status: pkgoauth.SessionServerStatusConnected}
		if token := api.FullTokenByIssuerForUser(oauthHandler, sessionID, sub, app.Issuer); token == nil || token.AccessToken == "" {
			status.Status = pkgoauth.SessionServerStatusAuthRequired
			status.AuthTool = "core_auth_login"
			if app.Flow == configPkg.ProviderAppFlowChatGPTDeviceCode {
				// A device code is issued by core_auth_login only: a status
				// read never starts a sign-in the person did not ask for.
				a.deviceCodeStatus(sub, &status)
			} else if withURLs {
				login := p.grantProviderApp(ctx, sessionID, sub, name, nil)
				if url := challengeURL(login); url != "" {
					status.AuthURL = url
				} else if login.IsError {
					status.Error = resultText(login)
				}
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}
