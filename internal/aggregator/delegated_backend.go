package aggregator

import (
	"fmt"
	"os"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// delegatedForwardRefusal returns why a delegated caller cannot reach a server
// that receives the caller's token as is (forwardToken without token exchange,
// or forwardIdentity), and nil otherwise. A delegated caller's token is
// muster's credential only: an MCP server must not pass the token it received
// on to another service (MCP authorization, Access Token Privilege
// Restriction), so such a server is reached only through token exchange.
func delegatedForwardRefusal(serverInfo *ServerInfo) error {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return nil
	}
	forwards := ShouldUseTokenForwarding(serverInfo) && !ShouldUseTokenExchange(serverInfo)
	if forwards || serverInfo.AuthConfig.ForwardsIdentity() {
		return fmt.Errorf("%s receives the caller's token as is (forwardToken or forwardIdentity), which a delegated caller's token never is; it needs spec.auth.tokenExchange", serverInfo.Name)
	}
	return nil
}

// delegatedCacheKey is the token-exchange cache key of a delegated caller: one
// exchanged token per muster session and server, since the session is the
// person and the actor chain together. The key leaves out the delegated token,
// so a later token of the same session, with another scope, reuses the
// exchanged token until it expires. That is safe because muster bounds the
// toolset itself on every call from the current token's scope, and Dex caps
// the exchanged token's lifetime at the delegated token's.
func delegatedCacheKey(sessionID, serverName string) string {
	return "delegated/" + sessionID + "/" + serverName
}

// withDelegation turns the resolved exchange for a server into an RFC 8693
// delegation of the caller's token: muster's workload token as actor_token,
// the server's single required audience as audience, and no connector_id.
// The exchange must go to the Dex that issued the caller's token, since only
// that Dex verifies it with its own keys.
func (a *AggregatorServer) withDelegation(caller api.DelegatedCaller, serverInfo *ServerInfo, resolved api.ResolvedTokenExchangeConfig) (api.ResolvedTokenExchangeConfig, error) {
	if expired, err := pkgoauth.IsExpired(caller.Token); expired {
		return resolved, expiredTokenError("delegated token", serverInfo.Name, err)
	}
	issuer := resolved.ExpectedIssuer
	if issuer == "" {
		issuer = strings.TrimSuffix(resolved.DexTokenEndpoint, "/token")
	}
	if strings.TrimSuffix(issuer, "/") != strings.TrimSuffix(caller.Issuer, "/") {
		return resolved, fmt.Errorf("%s exchanges at %s, but a delegated caller's token from %s can only be exchanged at its own issuer", serverInfo.Name, issuer, caller.Issuer)
	}
	audiences := serverInfo.AuthConfig.RequiredAudiences
	if len(audiences) > 1 {
		return resolved, fmt.Errorf("%s requires %d audiences; a delegated exchange names one", serverInfo.Name, len(audiences))
	}
	actorToken, err := a.delegationActorToken()
	if err != nil {
		return resolved, fmt.Errorf("token exchange for a delegated caller to %s: %w", serverInfo.Name, err)
	}
	delegation := &api.TokenExchangeDelegation{ActorToken: actorToken}
	if len(audiences) == 1 {
		delegation.Audience = audiences[0]
	}
	resolved.Delegation = delegation
	return resolved, nil
}

// DelegationExchangeConfig implements api.DelegationExchange: an RFC 8693
// delegation at muster's Dex, with muster's Dex client and its workload token
// as actor_token. The caller's token must come from muster's Dex, the only Dex
// that client is registered with; a token from another issuer gets an error.
func (a *AggregatorServer) DelegationExchangeConfig(caller api.DelegatedCaller) (api.TokenExchangeConfig, error) {
	cfg, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig)
	if !ok || cfg.Dex.IssuerURL == "" || cfg.Dex.ClientID == "" {
		return api.TokenExchangeConfig{}, fmt.Errorf("muster's Dex client is not configured")
	}
	issuer := strings.TrimSuffix(cfg.Dex.IssuerURL, "/")
	if issuer != strings.TrimSuffix(caller.Issuer, "/") {
		return api.TokenExchangeConfig{}, fmt.Errorf("the delegated token is from %s, not from muster's Dex %s", caller.Issuer, issuer)
	}
	actorToken, err := a.delegationActorToken()
	if err != nil {
		return api.TokenExchangeConfig{}, err
	}
	return api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: issuer + "/token",
		ExpectedIssuer:   issuer,
		ClientID:         cfg.Dex.ClientID,
		ClientSecret:     cfg.Dex.ClientSecret,
		Delegation:       &api.TokenExchangeDelegation{ActorToken: actorToken},
	}, nil
}

// delegationActorToken reads muster's workload token from
// oauth.server.delegation.actorTokenFile, on every exchange so a rotated
// projected token is picked up.
func (a *AggregatorServer) delegationActorToken() (string, error) {
	cfg, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig)
	if !ok || cfg.Delegation.ActorTokenFile == "" {
		return "", fmt.Errorf("oauth.server.delegation.actorTokenFile is not set")
	}
	raw, err := os.ReadFile(cfg.Delegation.ActorTokenFile)
	if err != nil {
		return "", fmt.Errorf("read the actor token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("the actor token file %s is empty", cfg.Delegation.ActorTokenFile)
	}
	return token, nil
}

// logDelegatedExchange records the outcome of exchanging a delegated caller's
// token for a server's token.
func logDelegatedExchange(caller api.DelegatedCaller, sessionID, serverName string, err error) {
	event := logging.AuditEvent{
		Action:  "delegated_token_exchange",
		Outcome: "success",
		Subject: logging.TruncateIdentifier(caller.Subject),
		Target:  serverName,
		Details: fmt.Sprintf("session=%s actors=[%s]", logging.TruncateIdentifier(sessionID), strings.Join(caller.ActorChain, ",")),
	}
	if err != nil {
		event.Outcome, event.Error = "failure", err.Error()
	}
	logging.Audit(event)
}
