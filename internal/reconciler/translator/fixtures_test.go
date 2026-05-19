package translator_test

import (
	"github.com/giantswarm/muster/internal/reconciler/translator"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

const testName = "my-server"

func authOAuth() *v1alpha1.MCPServerAuth {
	return &v1alpha1.MCPServerAuth{Type: "oauth"}
}

func authOAuthForward() *v1alpha1.MCPServerAuth {
	return &v1alpha1.MCPServerAuth{Type: "oauth", ForwardToken: true}
}

func authOAuthForwardAudiences() *v1alpha1.MCPServerAuth {
	return &v1alpha1.MCPServerAuth{
		Type:              "oauth",
		ForwardToken:      true,
		RequiredAudiences: []string{"dex-k8s", "platform-api"},
	}
}

func authOAuthTokenExchange() *v1alpha1.MCPServerAuth {
	return &v1alpha1.MCPServerAuth{
		Type: "oauth",
		TokenExchange: &v1alpha1.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.cluster-b.example.com/token",
			ExpectedIssuer:   "https://dex.cluster-b.example.com",
			ConnectorID:      "cluster-a-dex",
			Scopes:           "openid profile email groups",
			ClientCredentialsSecretRef: &v1alpha1.ClientCredentialsSecretRef{
				Name:      "grizzly-token-exchange-credentials",
				Namespace: "muster",
			},
		},
	}
}

func authOAuthAuthorizationServer() *v1alpha1.MCPServerAuth {
	return &v1alpha1.MCPServerAuth{
		Type: "oauth",
		AuthorizationServer: &v1alpha1.MCPServerAuthAuthorizationServer{
			Issuer: "https://auth.atlassian.com",
			Scopes: "read:jira-user offline_access",
		},
	}
}

func authTeleport() *v1alpha1.MCPServerAuth {
	return &v1alpha1.MCPServerAuth{
		Type: "teleport",
		Teleport: &v1alpha1.TeleportAuthConfig{
			IdentitySecretName:      "tbot-identity-output",
			IdentitySecretNamespace: "tbot",
			IdentityDir:             "/var/run/tbot/identity",
			AppName:                 "mcp-kubernetes",
		},
	}
}

func stdioSpec(auth *v1alpha1.MCPServerAuth) v1alpha1.MCPServerSpec {
	return v1alpha1.MCPServerSpec{
		Type:    "stdio",
		Command: "/usr/local/bin/mcp-child",
		Args:    []string{"--flag", "value"},
		Env:     map[string]string{"FOO": "bar"},
		Auth:    auth,
	}
}

func streamableSpec(auth *v1alpha1.MCPServerAuth) v1alpha1.MCPServerSpec {
	return v1alpha1.MCPServerSpec{
		Type: "streamable-http",
		URL:  "https://api.example.com/mcp",
		Auth: auth,
	}
}

func sseSpec(auth *v1alpha1.MCPServerAuth) v1alpha1.MCPServerSpec {
	return v1alpha1.MCPServerSpec{
		Type: "sse",
		URL:  "http://mcp.cluster.local:8080/sse",
		Auth: auth,
	}
}

// kind describes which Backend variant a happy case expects.
type kind int

const (
	kindStdio kind = iota
	kindStreamableHTTP
	kindSSE
)

type expectHTTP struct {
	host string
	port int
	path string
}

type expectStdio struct {
	command string
	args    []string
	env     map[string]string
}

type happyCase struct {
	name          string
	spec          v1alpha1.MCPServerSpec
	wantKind      kind
	wantHTTP      expectHTTP
	wantStdio     expectStdio
	wantAuthn     translator.AuthnConfig
	wantPathMatch string
}
