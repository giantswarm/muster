package oauth

import (
	"net/url"

	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/giantswarm/mcp-oauth/providers/oidc"
	"github.com/giantswarm/mcp-oauth/providers/tokencache"
)

func tokenExchangeCacheKey(request *ExchangeRequest) string {
	subjectType, scopes := getExchangeDefaults(request)
	values := url.Values{
		"user_id": {request.UserID}, "subject_token": {request.SubjectToken},
		"subject_token_type": {subjectType}, "scope": {scopes},
		"client_id": {request.Config.ClientID}, "client_secret": {request.Config.ClientSecret},
		"expected_issuer": {GetExpectedIssuer(request.Config)},
	}
	if delegation := request.Config.Delegation; delegation != nil {
		values.Set("scope", withoutCrossClientScopes(scopes))
		values.Set("subject_token_type", oidc.TokenTypeAccessToken)
		values.Set("actor_token", delegation.ActorToken)
		values.Set("audience", delegation.Audience)
	}
	return tokencache.GenerateCacheKey(request.Config.DexTokenEndpoint, request.Config.ConnectorID, values.Encode())
}

// ClearCache removes a cached token for the given exchange request.
func (e *TokenExchanger) ClearCache(request *ExchangeRequest) {
	if request == nil || request.Config == nil {
		return
	}
	e.cache.Delete(tokenExchangeCacheKey(request))
	logging.Debug("TokenExchange", "Cleared cache for user=%s endpoint=%s",
		logging.TruncateIdentifier(request.UserID), request.Config.DexTokenEndpoint)
}

// ClearAllCache removes all cached tokens.
func (e *TokenExchanger) ClearAllCache() {
	e.cache.Clear()
	logging.Debug("TokenExchange", "Cleared all cached tokens")
}

// GetCacheStats returns statistics about the token exchange cache.
func (e *TokenExchanger) GetCacheStats() tokencache.Stats {
	return e.cache.GetStats()
}

// Cleanup removes expired tokens from the cache.
func (e *TokenExchanger) Cleanup() int {
	removed := e.cache.Cleanup()
	if removed > 0 {
		logging.Debug("TokenExchange", "Cleaned up %d expired cached tokens", removed)
	}
	return removed
}
