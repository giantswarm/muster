package nativeconfig

// LocalConfig is the root object of an agentgateway YAML configuration file.
type LocalConfig struct {
	// Binds lists the L4 sockets agentgateway listens on.
	Binds []LocalBind `yaml:"binds,omitempty"`
}

// LocalBind binds a set of listeners to a TCP port.
type LocalBind struct {
	// Port is the TCP port agentgateway listens on.
	Port uint16 `yaml:"port"`
	// Listeners are the per-hostname configurations served on Port.
	Listeners []LocalListener `yaml:"listeners"`
}

// LocalListener is the agw listener that owns one or more HTTP routes.
type LocalListener struct {
	// Name is a stable identifier used in agw logs and metrics.
	Name string `yaml:"name,omitempty"`
	// Routes are the HTTP routes served by this listener.
	Routes []LocalRoute `yaml:"routes,omitempty"`
}

// LocalRoute attaches an HTTP path-match to a set of backends and policies.
type LocalRoute struct {
	// Name is a stable identifier used in agw logs and metrics.
	Name string `yaml:"name,omitempty"`
	// Matches are the request predicates that select this route.
	Matches []RouteMatch `yaml:"matches,omitempty"`
	// Backends are the upstream targets traffic flows to once matched.
	Backends []LocalRouteBackend `yaml:"backends,omitempty"`
	// Policies are the route-scoped filters and policies.
	Policies *FilterOrPolicy `yaml:"policies,omitempty"`
}

// RouteMatch is a single request predicate.
type RouteMatch struct {
	// Path matches the request path.
	Path *PathMatch `yaml:"path,omitempty"`
}

// PathMatch is the agw path-match polymorphic shape; exactly one field is set.
type PathMatch struct {
	// Exact requires the request path to equal this value.
	Exact string `yaml:"exact,omitempty"`
	// PathPrefix requires the request path to begin with this value.
	PathPrefix string `yaml:"pathPrefix,omitempty"`
	// Regex requires the request path to match this RE2 regular expression.
	Regex string `yaml:"regex,omitempty"`
}

// LocalRouteBackend is the per-route backend selector; exactly one variant is set.
type LocalRouteBackend struct {
	// MCP selects an inline MCP multiplexing backend.
	MCP *LocalMcpBackend `yaml:"mcp,omitempty"`
}

// LocalMcpBackend declares an MCP multiplexing backend.
type LocalMcpBackend struct {
	// Targets are the upstream MCP endpoints agw fans out to.
	Targets []LocalMcpTarget `yaml:"targets"`
}

// LocalMcpTarget is one MCP upstream; exactly one transport field is set.
type LocalMcpTarget struct {
	// Name identifies the target in agw logs, metrics and tool prefixes.
	Name string `yaml:"name"`
	// MCP carries the StreamableHTTP endpoint coordinates.
	MCP *McpTargetEndpoint `yaml:"mcp,omitempty"`
	// SSE carries the Server-Sent Events endpoint coordinates.
	SSE *McpTargetEndpoint `yaml:"sse,omitempty"`
	// Stdio carries the stdio child process specification. When set,
	// agentgateway spawns the configured command itself and exchanges
	// JSON-RPC frames over its stdin/stdout.
	Stdio *McpTargetStdio `yaml:"stdio,omitempty"`
}

// McpTargetEndpoint is the host/port/path tuple of an HTTP-based MCP target.
type McpTargetEndpoint struct {
	// Host is the DNS name or IP of the upstream MCP server.
	Host string `yaml:"host,omitempty"`
	// Port is the TCP port of the upstream MCP server.
	Port uint16 `yaml:"port,omitempty"`
	// Path is the URL path agw forwards requests to.
	Path string `yaml:"path,omitempty"`
}

// McpTargetStdio is the agentgateway-native stdio MCP target shape. Matches
// the LocalMcpTarget.stdio variant in agw's config schema: agw spawns Cmd
// with Args and Env and bridges JSON-RPC frames over the child's stdio.
type McpTargetStdio struct {
	// Cmd is the executable path or PATH-resolvable name agw spawns.
	Cmd string `yaml:"cmd"`
	// Args are the command-line arguments passed to Cmd, in order.
	Args []string `yaml:"args,omitempty"`
	// Env are KEY=VALUE entries prepended to the child's environment.
	Env map[string]string `yaml:"env,omitempty"`
}

// FilterOrPolicy carries the subset of agw route-scoped policies muster emits.
type FilterOrPolicy struct {
	// BackendAuth controls how agw authenticates to the upstream.
	BackendAuth *BackendAuth `yaml:"backendAuth,omitempty"`
}

// BackendAuth selects how agw authenticates to the upstream backend.
type BackendAuth struct {
	// Passthrough forwards the caller's bearer credential unchanged.
	Passthrough *Passthrough `yaml:"passthrough,omitempty"`
}

// Passthrough configures BackendAuth.Passthrough. An empty object accepts the
// agw defaults: forward the Authorization header verbatim.
type Passthrough struct{}
