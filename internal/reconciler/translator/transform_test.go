package translator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/translator"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func TestTransform_Happy(t *testing.T) {
	t.Parallel()

	stdioHTTP := expectHTTP{} // stdio backends have no HTTP target
	stdioBody := expectStdio{
		command: "/usr/local/bin/mcp-child",
		args:    []string{"--flag", "value"},
		env:     map[string]string{"FOO": "bar"},
	}
	streamableHTTP := expectHTTP{host: "api.example.com", port: 443, path: "/mcp"}
	sseHTTP := expectHTTP{host: "mcp.cluster.local", port: 8080, path: "/sse"}

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
		{name: "stdio/auth-nil", spec: stdioSpec(nil), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-none", spec: stdioSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth", spec: stdioSpec(authOAuth()), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-forward", spec: stdioSpec(authOAuthForward()), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-forward-audiences", spec: stdioSpec(authOAuthForwardAudiences()), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-token-exchange", spec: stdioSpec(authOAuthTokenExchange()), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-authorization-server", spec: stdioSpec(authOAuthAuthorizationServer()), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-teleport", spec: stdioSpec(authTeleport()), wantKind: kindStdio, wantHTTP: stdioHTTP, wantStdio: stdioBody, wantAuthn: authTeleportCfg, wantPathMatch: pathMatch},

		// streamable-http × 8 auth profiles
		{name: "streamable/auth-nil", spec: streamableSpec(nil), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-none", spec: streamableSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth", spec: streamableSpec(authOAuth()), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-forward", spec: streamableSpec(authOAuthForward()), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-forward-audiences", spec: streamableSpec(authOAuthForwardAudiences()), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-token-exchange", spec: streamableSpec(authOAuthTokenExchange()), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-authorization-server", spec: streamableSpec(authOAuthAuthorizationServer()), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-teleport", spec: streamableSpec(authTeleport()), wantKind: kindStreamableHTTP, wantHTTP: streamableHTTP, wantAuthn: authTeleportCfg, wantPathMatch: pathMatch},

		// sse × 8 auth profiles
		{name: "sse/auth-nil", spec: sseSpec(nil), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-none", spec: sseSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth", spec: sseSpec(authOAuth()), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-forward", spec: sseSpec(authOAuthForward()), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-forward-audiences", spec: sseSpec(authOAuthForwardAudiences()), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-token-exchange", spec: sseSpec(authOAuthTokenExchange()), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-authorization-server", spec: sseSpec(authOAuthAuthorizationServer()), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-teleport", spec: sseSpec(authTeleport()), wantKind: kindSSE, wantHTTP: sseHTTP, wantAuthn: authTeleportCfg, wantPathMatch: pathMatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := translator.Transform(testName, tc.spec)
			require.NoError(t, err)

			require.Len(t, got.Backends, 1, "exactly one backend")
			backend := got.Backends[0]
			require.Equal(t, testName, backend.Name)
			assertBackendVariant(t, backend, tc.wantKind, tc.wantHTTP, tc.wantStdio)

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
		})
	}
}

func assertBackendVariant(t *testing.T, b translator.Backend, want kind, http expectHTTP, stdio expectStdio) {
	t.Helper()
	switch want {
	case kindStdio:
		require.Nil(t, b.StreamableHTTP, "stdio backend must not have StreamableHTTP target")
		require.Nil(t, b.SSE, "stdio backend must not have SSE target")
		require.NotNil(t, b.Stdio, "stdio backend must have Stdio target")
		require.Equal(t, stdio.command, b.Stdio.Command)
		require.Equal(t, stdio.args, b.Stdio.Args)
		require.Equal(t, stdio.env, b.Stdio.Env)
	case kindStreamableHTTP:
		require.Nil(t, b.Stdio, "streamable-http backend must not have Stdio target")
		require.Nil(t, b.SSE, "streamable-http backend must not have SSE target")
		require.NotNil(t, b.StreamableHTTP, "streamable-http backend must have StreamableHTTP target")
		require.Equal(t, http.host, b.StreamableHTTP.Host)
		require.Equal(t, http.port, b.StreamableHTTP.Port)
		require.Equal(t, http.path, b.StreamableHTTP.Path)
	case kindSSE:
		require.Nil(t, b.Stdio, "sse backend must not have Stdio target")
		require.Nil(t, b.StreamableHTTP, "sse backend must not have StreamableHTTP target")
		require.NotNil(t, b.SSE, "sse backend must have SSE target")
		require.Equal(t, http.host, b.SSE.Host)
		require.Equal(t, http.port, b.SSE.Port)
		require.Equal(t, http.path, b.SSE.Path)
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
	require.NotNil(t, backend.StreamableHTTP)
	require.Nil(t, backend.SSE)
	require.Nil(t, backend.Stdio)
	require.Equal(t, "mcp-internal", backend.StreamableHTTP.Host)
	require.Equal(t, 9000, backend.StreamableHTTP.Port)
	require.Equal(t, "/api/v1/mcp", backend.StreamableHTTP.Path)
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
	require.NotNil(t, got.Backends[0].StreamableHTTP)
	require.Equal(t, 80, got.Backends[0].StreamableHTTP.Port)
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

	require.Len(t, got.Backends, 1)
	backend := got.Backends[0]
	require.NotNil(t, backend.Stdio, "stdio spec must produce a Stdio target")
	require.Nil(t, backend.StreamableHTTP)
	require.Nil(t, backend.SSE)
	require.Equal(t, spec.Command, backend.Stdio.Command)
	require.Equal(t, spec.Args, backend.Stdio.Args)
	require.Equal(t, spec.Env, backend.Stdio.Env)
}
