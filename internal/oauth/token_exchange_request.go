package oauth

import (
	"strings"

	"github.com/giantswarm/mcp-oauth/providers/oidc"
)

func getExchangeDefaults(request *ExchangeRequest) (tokenType, scopes string) {
	tokenType = request.SubjectTokenType
	if tokenType == "" {
		tokenType = oidc.TokenTypeIDToken
	}
	scopes = request.Config.Scopes
	if scopes == "" {
		scopes = DefaultOIDCScopes
	}
	return tokenType, scopes
}

// withoutCrossClientScopes removes Dex cross-client scopes because a delegated
// exchange identifies its target with the audience parameter.
func withoutCrossClientScopes(scopes string) string {
	var kept []string
	for _, scope := range strings.Fields(scopes) {
		if !strings.HasPrefix(scope, "audience:server:client_id:") {
			kept = append(kept, scope)
		}
	}
	return strings.Join(kept, " ")
}
