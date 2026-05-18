package agentgateway_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func TestCompile_Happy(t *testing.T) {
	t.Parallel()

	stdioBody := expectStdio{
		command: "/usr/local/bin/mcp-child",
		args:    []string{"--flag", "value"},
		env:     map[string]string{"FOO": "bar"},
	}
	streamableHTTP := expectHTTP{protocol: agentgateway.StreamableHTTP, host: "api.example.com", port: 443, path: "/mcp"}
	sseHTTP := expectHTTP{protocol: agentgateway.SSE, host: "mcp.cluster.local", port: 8080, path: "/sse"}

	authNoneCfg := agentgateway.Authn{Type: agentgateway.AuthnTypeNone}
	authOAuthCfg := agentgateway.Authn{Type: agentgateway.AuthnTypeOAuth}
	authOAuthForwardCfg := agentgateway.Authn{Type: agentgateway.AuthnTypeOAuth, ForwardToken: true}
	authOAuthForwardAudCfg := agentgateway.Authn{
		Type:              agentgateway.AuthnTypeOAuth,
		ForwardToken:      true,
		RequiredAudiences: []string{"dex-k8s", "platform-api"},
	}
	authOAuthTokenExCfg := agentgateway.Authn{
		Type: agentgateway.AuthnTypeOAuth,
		TokenExchange: &agentgateway.TokenExchange{
			Enabled:                          true,
			DexTokenEndpoint:                 "https://dex.cluster-b.example.com/token",
			ExpectedIssuer:                   "https://dex.cluster-b.example.com",
			ConnectorID:                      "cluster-a-dex",
			Scopes:                           "openid profile email groups",
			ClientCredentialsSecretName:      "grizzly-token-exchange-credentials",
			ClientCredentialsSecretNamespace: "muster",
		},
	}
	authOAuthASCfg := agentgateway.Authn{
		Type: agentgateway.AuthnTypeOAuth,
		AuthorizationServer: &agentgateway.AuthorizationServer{
			Issuer: "https://auth.atlassian.com",
			Scopes: "read:jira-user offline_access",
		},
	}

	pathMatch := "/mcp/" + testName

	cases := []happyCase{
		// stdio × 7 auth profiles
		{name: "stdio/auth-nil", spec: stdioSpec(nil), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-none", spec: stdioSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth", spec: stdioSpec(authOAuth()), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-forward", spec: stdioSpec(authOAuthForward()), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-forward-audiences", spec: stdioSpec(authOAuthForwardAudiences()), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-token-exchange", spec: stdioSpec(authOAuthTokenExchange()), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "stdio/auth-oauth-authorization-server", spec: stdioSpec(authOAuthAuthorizationServer()), wantKind: agentgateway.TargetStdio, wantStdio: stdioBody, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},

		// streamable-http × 7 auth profiles
		{name: "streamable/auth-nil", spec: streamableSpec(nil), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-none", spec: streamableSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth", spec: streamableSpec(authOAuth()), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-forward", spec: streamableSpec(authOAuthForward()), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-forward-audiences", spec: streamableSpec(authOAuthForwardAudiences()), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-token-exchange", spec: streamableSpec(authOAuthTokenExchange()), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "streamable/auth-oauth-authorization-server", spec: streamableSpec(authOAuthAuthorizationServer()), wantKind: agentgateway.TargetHTTP, wantHTTP: streamableHTTP, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},

		// sse × 7 auth profiles
		{name: "sse/auth-nil", spec: sseSpec(nil), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-none", spec: sseSpec(&v1alpha1.MCPServerAuth{Type: "none"}), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authNoneCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth", spec: sseSpec(authOAuth()), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authOAuthCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-forward", spec: sseSpec(authOAuthForward()), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authOAuthForwardCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-forward-audiences", spec: sseSpec(authOAuthForwardAudiences()), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authOAuthForwardAudCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-token-exchange", spec: sseSpec(authOAuthTokenExchange()), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authOAuthTokenExCfg, wantPathMatch: pathMatch},
		{name: "sse/auth-oauth-authorization-server", spec: sseSpec(authOAuthAuthorizationServer()), wantKind: agentgateway.TargetHTTP, wantHTTP: sseHTTP, wantAuthn: authOAuthASCfg, wantPathMatch: pathMatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := agentgateway.Compile(testName, tc.spec)
			require.NoError(t, err)

			require.Len(t, got.Backends, 1, "exactly one backend")
			backend := got.Backends[0]
			require.Equal(t, testName, backend.Name)
			assertBackendTarget(t, backend, tc.wantKind, tc.wantHTTP, tc.wantStdio)

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

func assertBackendTarget(t *testing.T, b agentgateway.Backend, wantKind agentgateway.TargetKind, http expectHTTP, stdio expectStdio) {
	t.Helper()
	require.NotNil(t, b.Target, "Backend.Target must be set")
	require.Equal(t, wantKind, b.Target.Kind())
	switch wantKind {
	case agentgateway.TargetHTTP:
		target, ok := b.Target.(agentgateway.HTTPTarget)
		require.True(t, ok, "Target must be HTTPTarget")
		require.Equal(t, http.protocol, target.Protocol)
		require.Equal(t, http.host, target.Host)
		require.Equal(t, http.port, target.Port)
		require.Equal(t, http.path, target.Path)
	case agentgateway.TargetStdio:
		target, ok := b.Target.(agentgateway.StdioTarget)
		require.True(t, ok, "Target must be StdioTarget")
		require.Equal(t, stdio.command, target.Command)
		require.Equal(t, stdio.args, target.Args)
		require.Equal(t, stdio.env, target.Env)
	}
}

func TestCompile_Errors(t *testing.T) {
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
		{
			name: "teleport-auth-type-rejected",
			in: func() (string, v1alpha1.MCPServerSpec) {
				return testName, streamableSpec(&v1alpha1.MCPServerAuth{Type: "teleport"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			name, spec := tc.in()
			_, err := agentgateway.Compile(name, spec)
			require.Error(t, err, "expected Compile to reject input")
		})
	}
}

func TestCompile_IsPure(t *testing.T) {
	t.Parallel()

	spec := streamableSpec(authOAuthForwardAudiences())

	first, err := agentgateway.Compile(testName, spec)
	require.NoError(t, err)
	second, err := agentgateway.Compile(testName, spec)
	require.NoError(t, err)

	require.Equal(t, first, second, "Compile must be deterministic")
}

func TestCompile_HTTPStreamableWithExplicitPort(t *testing.T) {
	t.Parallel()

	spec := v1alpha1.MCPServerSpec{
		Type: "streamable-http",
		URL:  "http://mcp-internal:9000/api/v1/mcp",
	}

	got, err := agentgateway.Compile(testName, spec)
	require.NoError(t, err)

	require.Len(t, got.Backends, 1)
	target, ok := got.Backends[0].Target.(agentgateway.HTTPTarget)
	require.True(t, ok)
	require.Equal(t, agentgateway.StreamableHTTP, target.Protocol)
	require.Equal(t, "mcp-internal", target.Host)
	require.Equal(t, 9000, target.Port)
	require.Equal(t, "/api/v1/mcp", target.Path)
}

func TestCompile_HTTPDefaultPort(t *testing.T) {
	t.Parallel()

	spec := v1alpha1.MCPServerSpec{
		Type: "streamable-http",
		URL:  "http://plain-http.example.com/mcp",
	}

	got, err := agentgateway.Compile(testName, spec)
	require.NoError(t, err)
	require.Len(t, got.Backends, 1)
	target, ok := got.Backends[0].Target.(agentgateway.HTTPTarget)
	require.True(t, ok)
	require.Equal(t, 80, target.Port)
}

func TestCompile_StdioPreservesArgsAndEnv(t *testing.T) {
	t.Parallel()

	spec := v1alpha1.MCPServerSpec{
		Type:    "stdio",
		Command: "/bin/mcp",
		Args:    []string{"--workdir", "/data", "--quiet"},
		Env:     map[string]string{"LOG_LEVEL": "debug", "TOKEN": "secret"},
	}

	got, err := agentgateway.Compile(testName, spec)
	require.NoError(t, err)

	require.Len(t, got.Backends, 1)
	target, ok := got.Backends[0].Target.(agentgateway.StdioTarget)
	require.True(t, ok, "stdio spec must produce a StdioTarget")
	require.Equal(t, spec.Command, target.Command)
	require.Equal(t, spec.Args, target.Args)
	require.Equal(t, spec.Env, target.Env)
}
