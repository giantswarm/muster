package translate

import (
	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// InfoToMCPServerSpec projects an api.MCPServerInfo onto the CRD spec the
// agentgateway translator consumes. The function is pure: same input yields
// the same output and no I/O occurs.
func InfoToMCPServerSpec(info *api.MCPServerInfo) musterv1alpha1.MCPServerSpec {
	spec := musterv1alpha1.MCPServerSpec{
		Type:        info.Type,
		ToolPrefix:  info.ToolPrefix,
		Description: info.Description,
		AutoStart:   info.AutoStart,
		Command:     info.Command,
		Args:        info.Args,
		URL:         info.URL,
		Env:         info.Env,
		Headers:     info.Headers,
		Timeout:     info.Timeout,
	}
	if info.Auth != nil {
		spec.Auth = MCPServerAuthFromAPI(info.Auth)
	}
	return spec
}

// MCPServerAuthFromAPI maps the API auth struct onto its CRD analog.
func MCPServerAuthFromAPI(auth *api.MCPServerAuth) *musterv1alpha1.MCPServerAuth {
	out := &musterv1alpha1.MCPServerAuth{
		Type:              auth.Type,
		ForwardToken:      auth.ForwardToken,
		RequiredAudiences: auth.RequiredAudiences,
	}
	if auth.TokenExchange != nil {
		out.TokenExchange = TokenExchangeFromAPI(auth.TokenExchange)
	}
	if auth.AuthorizationServer != nil {
		out.AuthorizationServer = &musterv1alpha1.MCPServerAuthAuthorizationServer{
			Issuer: musterv1alpha1.IssuerURL(auth.AuthorizationServer.Issuer),
			Scopes: auth.AuthorizationServer.Scopes,
		}
	}
	return out
}

// TokenExchangeFromAPI maps the API token-exchange config onto its CRD analog.
func TokenExchangeFromAPI(tx *api.TokenExchangeConfig) *musterv1alpha1.TokenExchangeConfig {
	out := &musterv1alpha1.TokenExchangeConfig{
		Enabled:          tx.Enabled,
		DexTokenEndpoint: tx.DexTokenEndpoint,
		ExpectedIssuer:   tx.ExpectedIssuer,
		ConnectorID:      tx.ConnectorID,
		Scopes:           tx.Scopes,
	}
	if tx.ClientCredentialsSecretRef != nil {
		out.ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{
			Name:            tx.ClientCredentialsSecretRef.Name,
			Namespace:       tx.ClientCredentialsSecretRef.Namespace,
			ClientIDKey:     tx.ClientCredentialsSecretRef.ClientIDKey,
			ClientSecretKey: tx.ClientCredentialsSecretRef.ClientSecretKey,
		}
	}
	return out
}
