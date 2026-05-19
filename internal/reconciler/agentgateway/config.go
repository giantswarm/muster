package agentgateway

// Config is the agentgateway configuration for a single MCPServer.
// Backends, Routes, and Policies share the MCPServer's name as their identifier.
// Namespace is the MCPServer's namespace; adapters use it to scope emitted
// resources (Kubernetes namespace, filesystem directory layout, …).
type Config struct {
	Name      string
	Namespace string
	Backends  []Backend
	Routes    []Route
	Policies  []Policy
}

// TargetKind identifies the upstream transport of a Backend's Target.
type TargetKind string

const (
	TargetHTTP  TargetKind = "http"
	TargetStdio TargetKind = "stdio"
)

// Target is the upstream MCP server bound to a Backend.
// Implementations: HTTPTarget, StdioTarget.
type Target interface {
	Kind() TargetKind
}

// HTTPProtocol distinguishes the two HTTP-based MCP transports.
type HTTPProtocol string

const (
	StreamableHTTP HTTPProtocol = "streamable-http"
	SSE            HTTPProtocol = "sse"
)

// HTTPTarget is an MCP-over-HTTP upstream in either StreamableHTTP or SSE flavour.
type HTTPTarget struct {
	Protocol HTTPProtocol
	Host     string
	Port     int
	Path     string
}

// Kind reports TargetHTTP.
func (HTTPTarget) Kind() TargetKind { return TargetHTTP }

// StdioTarget is an MCP child process agentgateway spawns directly.
type StdioTarget struct {
	Command string
	Args    []string
	Env     map[string]string
}

// Kind reports TargetStdio.
func (StdioTarget) Kind() TargetKind { return TargetStdio }

// Backend pairs an upstream Target with a stable Name shared with the
// matching Route and Policy.
type Backend struct {
	Name   string
	Target Target
}

// Route binds a gateway path-match to a Backend under a Policy.
type Route struct {
	Name       string
	PathMatch  string
	BackendRef string
	PolicyRef  string
}

// Policy declares the authentication contract for a Route.
type Policy struct {
	Name  string
	Authn Authn
}

// AuthnType selects the authentication strategy applied at the gateway.
type AuthnType string

const (
	AuthnTypeNone  AuthnType = "none"
	AuthnTypeOAuth AuthnType = "oauth"
)

// Authn is the gateway-side view of MCPServerSpec.Auth.
// TokenExchange and AuthorizationServer are mutually exclusive and
// only valid when Type is AuthnTypeOAuth.
//
// RequiredAudiences is consumer-trusted: NewConfig forwards the CRD slice
// verbatim with no deduplication, ordering, or emptiness normalization.
// Callers that need a canonical form must impose it themselves.
//
// Trap: leaving MCPServerSpec.Auth.Type empty while populating
// AuthorizationServer is rejected at NewConfig time — the type discriminator
// must be set explicitly to AuthnTypeOAuth for AuthorizationServer to take
// effect. CRD validation does not enforce this today.
type Authn struct {
	Type                AuthnType
	ForwardToken        bool
	RequiredAudiences   []string
	TokenExchange       *TokenExchange
	AuthorizationServer *AuthorizationServer
}

// TokenExchange configures RFC 8693 token exchange against an upstream IdP.
type TokenExchange struct {
	Enabled                          bool
	DexTokenEndpoint                 string
	ExpectedIssuer                   string
	ConnectorID                      string
	Scopes                           string
	ClientCredentialsSecretName      string
	ClientCredentialsSecretNamespace string
}

// AuthorizationServer pins the OAuth authorization server for a backend
// whose protected resource does not publish RFC 9728 metadata.
type AuthorizationServer struct {
	Issuer string
	Scopes string
}
