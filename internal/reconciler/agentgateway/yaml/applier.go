package yaml

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"

	goyaml "gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

// SchemaURL pins the agentgateway native config schema this applier targets.
const SchemaURL = "https://raw.githubusercontent.com/agentgateway/agentgateway/refs/tags/v1.2.1/schema/config.json"

// DefaultListenerPort is the TCP port written into the bind block when no
// override is supplied via WithListenerPort.
const DefaultListenerPort uint16 = 8080

// DefaultListenerName is the stable name of the listener that owns every
// per-MCPServer route in the combined config.
const DefaultListenerName = "muster"

// ConfigFilename is the basename of the single combined agentgateway config
// file emitted into the configured directory. agentgateway's `-f` flag points
// at this file.
const ConfigFilename = "agentgateway.yaml"

const (
	pragma         = "# yaml-language-server: $schema=" + SchemaURL + "\n"
	tempFilename   = ConfigFilename + ".tmp"
	routePathRoot  = "/mcp/"
	dirPermissions = 0o755
	filePermission = 0o600
	maxNameLen     = 253
)

// nameSafe restricts route identifiers to the Kubernetes DNS subdomain shape
// (RFC 1123 labels joined by dots) so callers cannot inject yaml or shell
// metacharacters via MCPServer names.
var nameSafe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// Option configures Applier at construction time.
type Option func(*Applier)

// WithListenerPort overrides the bind port written into the combined file.
func WithListenerPort(port uint16) Option {
	return func(a *Applier) { a.listenerPort = port }
}

// WithAdminAddr overrides agentgateway's admin / stats / readiness listener
// addresses written into the combined config. Empty values leave the
// agentgateway defaults (127.0.0.1:15000, [::]:15020, [::]:15021) in
// place. Required for parallel muster instances; without per-instance
// overrides, only the first instance's agentgateway binds the management
// ports.
func WithAdminAddr(admin, stats, readiness string) Option {
	return func(a *Applier) {
		a.adminAddr = admin
		a.statsAddr = stats
		a.readinessAddr = readiness
	}
}

// Applier maintains the agentgateway native config as a single combined file
// at <dir>/agentgateway.yaml. Every Apply replaces the route for that
// MCPServer, every Delete removes one, and the file is rewritten atomically so
// agentgateway never observes a half-merged config. The zero value is not
// usable; construct via NewApplier.
type Applier struct {
	root          *os.Root
	listenerPort  uint16
	listenerName  string
	adminAddr     string
	statsAddr     string
	readinessAddr string

	mu     sync.Mutex
	routes map[string]LocalRoute
}

// NewApplier returns an Applier that writes <dir>/agentgateway.yaml. The
// directory is created with 0755 permissions if it does not exist, then opened
// as an os.Root so file operations are confined to that subtree. The file is
// initialised to an empty (but valid) agentgateway config — one bind, one
// listener, zero routes — so agentgateway can be started against it before any
// MCPServer has been reconciled. Subsequent Apply / Delete calls rewrite the
// file with the accumulated in-memory state.
func NewApplier(dir string, opts ...Option) (*Applier, error) {
	if dir == "" {
		return nil, fmt.Errorf("yaml applier: dir is required")
	}
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("yaml applier: mkdir %q: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("yaml applier: open root %q: %w", dir, err)
	}
	a := &Applier{
		root:         root,
		listenerPort: DefaultListenerPort,
		listenerName: DefaultListenerName,
		routes:       make(map[string]LocalRoute),
	}
	for _, opt := range opts {
		opt(a)
	}
	a.mu.Lock()
	if err := a.writeLocked(); err != nil {
		a.mu.Unlock()
		_ = root.Close()
		return nil, fmt.Errorf("yaml applier: write initial %s: %w", ConfigFilename, err)
	}
	a.mu.Unlock()
	return a, nil
}

// Close releases the underlying directory handle. Subsequent Apply and Delete
// calls return os.ErrClosed.
func (a *Applier) Close() error { return a.root.Close() }

// Apply registers the per-MCPServer route derived from config and rewrites the
// combined agentgateway.yaml. It satisfies agentgateway.Applier.
func (a *Applier) Apply(ctx context.Context, config agentgateway.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := nameFromConfig(config)
	if err != nil {
		return fmt.Errorf("yaml applier: %w", err)
	}
	if !isSafeName(name) {
		return fmt.Errorf("yaml applier: name %q is not a safe route identifier", name)
	}

	route, err := buildLocalRoute(name, config)
	if err != nil {
		return fmt.Errorf("yaml applier: build route for %q: %w", name, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.routes[name] = route
	return a.writeLocked()
}

// Delete removes the route for name from the combined config and rewrites
// agentgateway.yaml. It is a no-op when no route exists for that name.
func (a *Applier) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isSafeName(name) {
		return fmt.Errorf("yaml applier: name %q is not a safe route identifier", name)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.routes[name]; !ok {
		return nil
	}
	delete(a.routes, name)
	return a.writeLocked()
}

func (a *Applier) writeLocked() error {
	cfg := a.buildConfig()
	payload, err := marshalConfig(cfg)
	if err != nil {
		return fmt.Errorf("yaml applier: marshal combined config: %w", err)
	}
	if existing, err := a.root.ReadFile(ConfigFilename); err == nil && bytes.Equal(existing, payload) {
		return nil
	}
	if err := a.writeAtomic(payload); err != nil {
		return fmt.Errorf("yaml applier: write %s: %w", ConfigFilename, err)
	}
	return nil
}

func (a *Applier) buildConfig() *LocalConfig {
	cfg := &LocalConfig{}
	if a.adminAddr != "" || a.statsAddr != "" || a.readinessAddr != "" {
		cfg.Config = &LocalManagementConfig{
			AdminAddr:     a.adminAddr,
			StatsAddr:     a.statsAddr,
			ReadinessAddr: a.readinessAddr,
		}
	}
	if len(a.routes) == 0 {
		return cfg
	}
	names := make([]string, 0, len(a.routes))
	for name := range a.routes {
		names = append(names, name)
	}
	sort.Strings(names)
	routes := make([]LocalRoute, 0, len(names))
	for _, name := range names {
		routes = append(routes, a.routes[name])
	}
	cfg.Binds = []LocalBind{{
		Port: a.listenerPort,
		Listeners: []LocalListener{{
			Name:   a.listenerName,
			Routes: routes,
		}},
	}}
	return cfg
}

func isSafeName(name string) bool {
	if name == "" || len(name) > maxNameLen {
		return false
	}
	return nameSafe.MatchString(name)
}

func nameFromConfig(c agentgateway.Config) (string, error) {
	if len(c.Backends) != 1 || len(c.Routes) != 1 || len(c.Policies) != 1 {
		return "", fmt.Errorf("config must declare exactly one backend, route and policy (got %d/%d/%d)",
			len(c.Backends), len(c.Routes), len(c.Policies))
	}
	name := c.Backends[0].Name
	if name == "" {
		return "", fmt.Errorf("backend name is empty")
	}
	if c.Routes[0].Name != name {
		return "", fmt.Errorf("route name %q does not match backend name %q", c.Routes[0].Name, name)
	}
	if c.Policies[0].Name != name {
		return "", fmt.Errorf("policy name %q does not match backend name %q", c.Policies[0].Name, name)
	}
	return name, nil
}

func buildLocalRoute(name string, c agentgateway.Config) (LocalRoute, error) {
	backend := c.Backends[0]
	route := c.Routes[0]
	policy := c.Policies[0]

	target, err := targetFromBackend(backend)
	if err != nil {
		return LocalRoute{}, err
	}

	pathPrefix := route.PathMatch
	if pathPrefix == "" {
		pathPrefix = routePathRoot + name
	}

	return LocalRoute{
		Name: route.Name,
		Matches: []RouteMatch{
			{Path: &PathMatch{PathPrefix: pathPrefix}},
		},
		Backends: []LocalRouteBackend{
			{MCP: &LocalMcpBackend{Targets: []LocalMcpTarget{target}}},
		},
		Policies: policyFor(policy),
	}, nil
}

func targetFromBackend(b agentgateway.Backend) (LocalMcpTarget, error) {
	target := LocalMcpTarget{Name: b.Name}
	switch t := b.Target.(type) {
	case agentgateway.HTTPTarget:
		ep, err := httpEndpoint(b.Name, t)
		if err != nil {
			return LocalMcpTarget{}, err
		}
		switch t.Protocol {
		case agentgateway.SSE:
			target.SSE = ep
		default:
			target.MCP = ep
		}
	case agentgateway.StdioTarget:
		if t.Command == "" {
			return LocalMcpTarget{}, fmt.Errorf("backend %q stdio target has no command", b.Name)
		}
		target.Stdio = &McpTargetStdio{
			Cmd:  t.Command,
			Args: t.Args,
			Env:  t.Env,
		}
	default:
		return LocalMcpTarget{}, fmt.Errorf("backend %q has no transport target", b.Name)
	}
	return target, nil
}

func httpEndpoint(name string, t agentgateway.HTTPTarget) (*McpTargetEndpoint, error) {
	if t.Host == "" {
		return nil, fmt.Errorf("backend %q has unresolved host", name)
	}
	if t.Port <= 0 || t.Port > 65535 {
		return nil, fmt.Errorf("backend %q has out-of-range port %d", name, t.Port)
	}
	return &McpTargetEndpoint{
		Host: t.Host,
		Port: uint16(t.Port),
		Path: t.Path,
	}, nil
}

func policyFor(p agentgateway.Policy) *FilterOrPolicy {
	if !p.Authn.ForwardToken {
		return nil
	}
	return &FilterOrPolicy{
		BackendAuth: &BackendAuth{Passthrough: &Passthrough{}},
	}
}

func marshalConfig(cfg *LocalConfig) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.WriteString(pragma); err != nil {
		return nil, err
	}
	enc := goyaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeAtomic writes payload to agentgateway.yaml.tmp inside the root, fsyncs
// it, and renames it over agentgateway.yaml. The rename is atomic on POSIX
// file systems so concurrent readers never observe a half-written file.
func (a *Applier) writeAtomic(payload []byte) error {
	f, err := a.root.OpenFile(tempFilename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePermission)
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = a.root.Remove(tempFilename)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = a.root.Remove(tempFilename)
		return err
	}
	if err := f.Close(); err != nil {
		_ = a.root.Remove(tempFilename)
		return err
	}
	if err := a.root.Rename(tempFilename, ConfigFilename); err != nil {
		_ = a.root.Remove(tempFilename)
		return err
	}
	if dirFile, err := a.root.Open("."); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
