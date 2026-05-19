package yaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"

	goyaml "gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

// SchemaURL pins the agentgateway native config schema this applier targets.
const SchemaURL = "https://raw.githubusercontent.com/agentgateway/agentgateway/refs/tags/v1.2.1/schema/config.json"

// DefaultListenerPort is the TCP port written into every bind block when no
// override is supplied via WithListenerPort.
const DefaultListenerPort uint16 = 8080

const (
	pragma         = "# yaml-language-server: $schema=" + SchemaURL + "\n"
	fileExt        = ".yaml"
	tempExt        = ".yaml.tmp"
	routePathRoot  = "/mcp/"
	dirPermissions = 0o755
	filePermission = 0o600
	maxNameLen     = 253
)

// nameSafe restricts emitted file basenames to the Kubernetes DNS subdomain
// shape (RFC 1123 labels joined by dots) so the applier cannot be coerced into
// writing outside its configured directory.
var nameSafe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// Option configures Applier at construction time.
type Option func(*Applier)

// WithListenerPort overrides the bind port written into every emitted file.
func WithListenerPort(port uint16) Option {
	return func(a *Applier) { a.listenerPort = port }
}

// Applier serializes agentgateway.Config as agw native YAML files inside a
// configured directory. The zero value is not usable; construct via NewApplier.
type Applier struct {
	root         *os.Root
	listenerPort uint16

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewApplier returns an Applier that writes into dir. The directory is created
// with 0755 permissions if it does not exist, then opened as an os.Root so all
// subsequent file operations are confined to that subtree.
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
		locks:        make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Close releases the underlying directory handle. Subsequent Apply and Delete
// calls return os.ErrClosed.
func (a *Applier) Close() error { return a.root.Close() }

// Apply writes one agw native YAML file per MCPServer to the configured
// directory. It satisfies agentgateway.Applier.
func (a *Applier) Apply(ctx context.Context, config agentgateway.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := nameFromConfig(config)
	if err != nil {
		return fmt.Errorf("yaml applier: %w", err)
	}
	if !isSafeName(name) {
		return fmt.Errorf("yaml applier: name %q is not a safe filename component", name)
	}

	cfg, err := buildLocalConfig(name, config, a.listenerPort)
	if err != nil {
		return fmt.Errorf("yaml applier: build config for %q: %w", name, err)
	}

	payload, err := marshalConfig(cfg)
	if err != nil {
		return fmt.Errorf("yaml applier: marshal %q: %w", name, err)
	}

	mu := a.lockFor(name)
	mu.Lock()
	defer mu.Unlock()

	targetName := name + fileExt
	if existing, err := a.root.ReadFile(targetName); err == nil && bytes.Equal(existing, payload) {
		return nil
	}
	if err := a.writeAtomic(name, payload); err != nil {
		return fmt.Errorf("yaml applier: write %q: %w", targetName, err)
	}
	return nil
}

// Delete removes the file backing the named MCPServer. It is a no-op when no
// file exists for that name.
func (a *Applier) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isSafeName(name) {
		return fmt.Errorf("yaml applier: name %q is not a safe filename component", name)
	}

	mu := a.lockFor(name)
	mu.Lock()
	defer mu.Unlock()

	if err := a.root.Remove(name + fileExt); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("yaml applier: remove %q: %w", name+fileExt, err)
	}
	_ = a.root.Remove(name + tempExt)
	return nil
}

func (a *Applier) lockFor(name string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	mu, ok := a.locks[name]
	if !ok {
		mu = &sync.Mutex{}
		a.locks[name] = mu
	}
	return mu
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

func buildLocalConfig(name string, c agentgateway.Config, port uint16) (*LocalConfig, error) {
	backend := c.Backends[0]
	route := c.Routes[0]
	policy := c.Policies[0]

	target, err := targetFromBackend(backend)
	if err != nil {
		return nil, err
	}

	pathPrefix := route.PathMatch
	if pathPrefix == "" {
		pathPrefix = routePathRoot + name
	}

	emittedRoute := LocalRoute{
		Name: route.Name,
		Matches: []RouteMatch{
			{Path: &PathMatch{PathPrefix: pathPrefix}},
		},
		Backends: []LocalRouteBackend{
			{MCP: &LocalMcpBackend{Targets: []LocalMcpTarget{target}}},
		},
		Policies: policyFor(policy),
	}

	return &LocalConfig{
		Binds: []LocalBind{{
			Port: port,
			Listeners: []LocalListener{{
				Name:   name,
				Routes: []LocalRoute{emittedRoute},
			}},
		}},
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

// writeAtomic writes payload to <name>.yaml.tmp inside the root, fsyncs it,
// and renames it over <name>.yaml. The rename is atomic on POSIX file
// systems so concurrent readers never observe a half-written file.
func (a *Applier) writeAtomic(name string, payload []byte) error {
	tmpName := name + tempExt
	targetName := name + fileExt

	f, err := a.root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePermission)
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = a.root.Remove(tmpName)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = a.root.Remove(tmpName)
		return err
	}
	if err := f.Close(); err != nil {
		_ = a.root.Remove(tmpName)
		return err
	}
	if err := a.root.Rename(tmpName, targetName); err != nil {
		_ = a.root.Remove(tmpName)
		return err
	}
	if dirFile, err := a.root.Open("."); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
