package translator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/translator"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func TestTransform_Happy(t *testing.T) {
	t.Parallel()

	stdioBackend := expectBackend{host: "", port: 0, path: "/mcp", protocol: translator.ProtocolStreamableHTTP}
	stdioShim := &expectShim{
		command: "/usr/local/bin/mcp-child",
		args:    []string{"--flag", "value"},
		env:     map[string]string{"FOO": "bar"},
	}
	streamableBackend := expectBackend{host: "api.example.com", port: 443, path: "/mcp", protocol: translator.ProtocolStreamableHTTP}
	sseBackend := expectBackend{host: "mcp.cluster.local", port: 8080, path: "/sse", protocol: translator.ProtocolSSE}

	authNoneCfg := translator.AuthnConfig{Type: translator.AuthnTypeNone}
	authOAuthCfg := translator.AuthnConfig{Type: translator.AuthnTypeOAuth}
	authOAuthForwardCfg := translator.AuthnConfig{Type: translator.AuthnTypeOAuth, ForwardToken: true}
	authOAuthForwardAudCfg := translator.AuthnConfig{
		Type:              translator.AuthnTypeOAuth,
		ForwardToken:      true,
		RequiredAudiences: []string{"dex-k8s", "platform-api"},
	}
	authOAuthTokenExCfg := translator.AuthnConfig{
		Type: translator.AuthnTypeOAuth,
		TokenExchange: &translator.TokenExchangeAuthn{
			Enabled:                          true,
			DexTokenEndpoint:                 "https://dex.cluster-b.example.com/token",
			ExpectedIssuer:                   "https://dex.cluster-b.example.com",
			ConnectorID:                      "cluster-a-dex",
			Scopes:                           "openid profile email groups",
			ClientCredentialsSecretName:      "grizzly-token-exchange-credentials",
			ClientCredentialsSecretNamespace: "muster",
		},
	}
	authOAuthASCfg := translator.AuthnConfig{
		Type: translator.AuthnTypeOAuth,
		AuthorizationServer: &translator.AuthorizationServer{
			Issuer: "https://auth.atlassian.com",
			Scopes: "read:jira-user offline_access",
		},
	}
	authTeleportCfg := translator.AuthnConfig{
		Type: translator.AuthnTypeTeleport,
		Teleport: &translator.TeleportAuthn{
			IdentitySecretName:      "tbot-identity-output",
			IdentitySecretNamespace: "tbot",
			IdentityDir:             "/var/run/tbot/identity",
			AppName:                 "mcp-kubernetes",
		},
	}

	pathMatch := "/mcp/" + testName

	cases := []happyCase{
		// stdio × 8 auth profiles
		{name: "stdio/auth-nil", spec: stdioSpec(nil), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-none", spec: stdioSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth", spec: stdioSpec(authOAuth()), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-forward", spec: stdioSpec(authOAuthForward()), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-forward-audiences", spec: stdioSpec(authOAuthForwardAudiences()), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-token-exchange", spec: stdioSpec(authOAuthTokenExchange()), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-authorization-server", spec: stdioSpec(authOAuthAuthorizationServer()), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-teleport", spec: stdioSpec(authTeleport()), wantBackend: stdioBackend, wantShim: stdioShim, wantAuthn: authTeleportCfg, wantPathMatch: pathMatch},

		// streamable-http × 8 auth profiles
		{name: "streamable/auth-nil", spec: streamableSpec(nil), wantBackend: streamableBackend, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-none", spec: streamableSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantBackend: streamableBackend, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth", spec: streamableSpec(authOAuth()), wantBackend: streamableBackend, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-forward", spec: streamableSpec(authOAuthForward()), wantBackend: streamableBackend, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-forward-audiences", spec: streamableSpec(authOAuthForwardAudiences()), wantBackend: streamableBackend, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-token-exchange", spec: streamableSpec(authOAuthTokenExchange()), wantBackend: streamableBackend, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-authorization-server", spec: streamableSpec(authOAuthAuthorizationServer()), wantBackend: streamableBackend, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-teleport", spec: streamableSpec(authTeleport()), wantBackend: streamableBackend, wantAuthn: authTeleportCfg, wantPathMatch: pathMatch},

		// sse × 8 auth profiles
		{name: "sse/auth-nil", spec: sseSpec(nil), wantBackend: sseBackend, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-none", spec: sseSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantBackend: sseBackend, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth", spec: sseSpec(authOAuth()), wantBackend: sseBackend, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-forward", spec: sseSpec(authOAuthForward()), wantBackend: sseBackend, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-forward-audiences", spec: sseSpec(authOAuthForwardAudiences()), wantBackend: sseBackend, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-token-exchange", spec: sseSpec(authOAuthTokenExchange()), wantBackend: sseBackend, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-authorization-server", spec: sseSpec(authOAuthAuthorizationServer()), wantBackend: sseBackend, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-teleport", spec: sseSpec(authTeleport()), wantBackend: sseBackend, wantAuthn: authTeleportCfg, wantPathMatch: pathMatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := translator.Transform(testName, tc.spec)
			require.NoError(t, err)

			require.Len(t, got.Backends, 1, "exactly one backend")
			backend := got.Backends[0]
			require.Equal(t, testName, backend.Name)
			require.Equal(t, tc.wantBackend.host, backend.Host)
			require.Equal(t, tc.wantBackend.port, backend.Port)
			require.Equal(t, tc.wantBackend.path, backend.Path)
			require.Equal(t, tc.wantBackend.protocol, backend.Protocol)

			require.Len(t, got.Routes, 1, "exactly one route")
			route := got.Routes[0]
			require.Equal(t, testName, route.Name)
			require.Equal(t, tc.wantPathMatch, route.PathMatch)
			require.Equal(t, testName, route.BackendRef)
			require.Equal(t, testName, route.PolicyRef)

			require.Len(t, got.Policies, 1, "exactly one policy")
			policy := got.Policies[0]
			require.Equal(t, testName, policy.Name)
			require.Equal(t, tc.wantAuthn, policy.Authn)

			if tc.wantShim == nil {
				require.Empty(t, got.Shims, "non-stdio must not emit shims")
				return
			}
			require.Len(t, got.Shims, 1, "stdio must emit exactly one shim")
			shim := got.Shims[0]
			require.Equal(t, testName, shim.Name)
			require.Equal(t, tc.wantShim.command, shim.Command)
			require.Equal(t, tc.wantShim.args, shim.Args)
			require.Equal(t, tc.wantShim.env, shim.Env)
		})
	}
}

func TestTransform_Errors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   func() (string, v1alpha1.MCPServerSpec)
	}{
		{
			name: "empty-name",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return "", streamableSpec(nil)
			},
		},
		{
			name: "unknown-spec-type",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, v1alpha1.MCPServerSpec{Type: "grpc", URL: "https://x.example.com"}
			},
		},
		{
			name: "stdio-missing-command",
			in: func() (string, v1alpha1.MCPServerSpec) {
				s := stdioSpec(nil)
				s.Command = ""
				return testName, s
			},
		},
		{
			name: "streamable-missing-url",
			in: func() (string, v1alpha1.MCPServerSpec) {
				s := streamableSpec(nil)
				s.URL = ""
				return testName, s
			},
		},
		{
			name: "sse-missing-url",
			in: func() (string, v1alpha1.MCPServerSpec) {
				s := sseSpec(nil)
				s.URL = ""
				return testName, s
			},
		},
		{
			name: "streamable-invalid-url",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, v1alpha1.MCPServerSpec{Type: "streamable-http", URL: "::not a url"}
			},
		},
		{
			name: "streamable-unsupported-scheme",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, v1alpha1.MCPServerSpec{Type: "streamable-http", URL: "ftp://example.com/mcp"}
			},
		},
		{
			name: "streamable-missing-host",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, v1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https:///mcp"}
			},
		},
		{
			name: "streamable-non-numeric-port",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, v1alpha1.MCPServerSpec{Type: "streamable-http", URL: "http://host:abc/mcp"}
			},
		},
		{
			name: "forward-token-with-authorization-server",
			in: func() (string, v1alpha1.MCPServerSpec) {
				auth := authOAuthForward()
				auth.AuthorizationServer = &v1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://issuer.example.com"}
				return testName, streamableSpec(auth)
			},
		},
		{
			name: "token-exchange-with-authorization-server",
			in: func() (string, v1alpha1.MCPServerSpec) {
				auth := authOAuthTokenExchange()
				auth.AuthorizationServer = &v1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://issuer.example.com"}
				return testName, streamableSpec(auth)
			},
		},
		{
			name: "authorization-server-with-teleport-type",
			in: func() (string, v1alpha1.MCPServerSpec) {
				auth := authTeleport()
				auth.AuthorizationServer = &v1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://issuer.example.com"}
				return testName, streamableSpec(auth)
			},
		},
		{
			name: "authorization-server-with-none-type",
			in: func() (string, v1alpha1.MCPServerSpec) {
				auth := &v1alpha1.MCPServerAuth{
					Type:                "none",
					AuthorizationServer: &v1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://issuer.example.com"},
				}
				return testName, streamableSpec(auth)
			},
		},
		{
			name: "unknown-auth-type",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, streamableSpec(&v1alpha1.MCPServerAuth{Type: "mtls"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			name, spec := tc.in()
			_, err := translator.Transform(name, spec)
			require.Error(t, err, "expected Transform to reject input")
		})
	}
}

func TestTransform_IsPure(t *testing.T) {
	t.Parallel()

	spec := streamableSpec(authOAuthForwardAudiences())

	first, err := translator.Transform(testName, spec)
	require.NoError(t, err)
	second, err := translator.Transform(testName, spec)
	require.NoError(t, err)

	require.Equal(t, first, second, "Transform must be deterministic")
}

func TestTransform_HTTPStreamableWithExplicitPort(t *testing.T) {
	t.Parallel()

	spec := v1alpha1.MCPServerSpec{
		Type: "streamable-http",
		URL:  "http://mcp-internal:9000/api/v1/mcp",
	}

	got, err := translator.Transform(testName, spec)
	require.NoError(t, err)

	require.Len(t, got.Backends, 1)
	backend := got.Backends[0]
	require.Equal(t, "mcp-internal", backend.Host)
	require.Equal(t, 9000, backend.Port)
	require.Equal(t, "/api/v1/mcp", backend.Path)
	require.Equal(t, translator.ProtocolStreamableHTTP, backend.Protocol)
}

func TestTransform_HTTPDefaultPort(t *testing.T) {
	t.Parallel()

	spec := v1alpha1.MCPServerSpec{
		Type: "streamable-http",
		URL:  "http://plain-http.example.com/mcp",
	}

	got, err := translator.Transform(testName, spec)
	require.NoError(t, err)
	require.Len(t, got.Backends, 1)
	require.Equal(t, 80, got.Backends[0].Port)
}

func TestTransform_StdioPreservesArgsAndEnv(t *testing.T) {
	t.Parallel()

	spec := v1alpha1.MCPServerSpec{
		Type:    "stdio",
		Command: "/bin/mcp",
		Args:    []string{"--workdir", "/data", "--quiet"},
		Env:     map[string]string{"LOG_LEVEL": "debug", "TOKEN": "secret"},
	}

	got, err := translator.Transform(testName, spec)
	require.NoError(t, err)
	require.Len(t, got.Shims, 1)
	shim := got.Shims[0]
	require.Equal(t, spec.Command, shim.Command)
	require.Equal(t, spec.Args, shim.Args)
	require.Equal(t, spec.Env, shim.Env)

	require.Len(t, got.Backends, 1)
	require.Empty(t, got.Backends[0].Host)
	require.Zero(t, got.Backends[0].Port)
}
