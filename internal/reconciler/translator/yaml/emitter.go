package yaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sync"

	goyaml "gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/internal/agentgateway/configtypes"
	"github.com/giantswarm/muster/internal/reconciler/translator"
)

// SchemaURL pins the agentgateway native config schema this emitter targets.
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
)

// nameSafe restricts emitted file basenames to the Kubernetes DNS subdomain
// shape (RFC 1123 labels joined by dots) so the emitter cannot be coerced
// into writing outside its configured directory.
var nameSafe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

const maxNameLen = 253

// Option configures Emitter at construction time.
type Option func(*Emitter)

// WithListenerPort overrides the bind port written into every emitted file.
func WithListenerPort(port uint16) Option {
	return func(e *Emitter) { e.listenerPort = port }
}

// Emitter serializes translator Models as agw native YAML files inside a
// configured directory. The zero value is not usable; construct via New.
type Emitter struct {
	root         *os.Root
	listenerPort uint16

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns an Emitter that writes into dir. The directory is created with
// 0755 permissions if it does not exist, then opened as an os.Root so all
// subsequent file operations are confined to that subtree.
func New(dir string, opts ...Option) (*Emitter, error) {
	if dir == "" {
		return nil, fmt.Errorf("yaml emitter: dir is required")
	}
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("yaml emitter: mkdir %q: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("yaml emitter: open root %q: %w", dir, err)
	}
	e := &Emitter{
		root:         root,
		listenerPort: DefaultListenerPort,
		locks:        make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Close releases the underlying directory handle. Subsequent Emit and Delete
// calls return os.ErrClosed.
func (e *Emitter) Close() error { return e.root.Close() }

// Emit writes one agw native YAML file per MCPServer to the configured
// directory. It satisfies translator.ConfigEmitter.
func (e *Emitter) Emit(ctx context.Context, m translator.Model) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := nameFromModel(m)
	if err != nil {
		return fmt.Errorf("yaml emitter: %w", err)
	}
	if !isSafeName(name) {
		return fmt.Errorf("yaml emitter: name %q is not a safe filename component", name)
	}

	cfg, err := buildLocalConfig(name, m, e.listenerPort)
	if err != nil {
		return fmt.Errorf("yaml emitter: build config for %q: %w", name, err)
	}

	payload, err := marshalConfig(cfg)
	if err != nil {
		return fmt.Errorf("yaml emitter: marshal %q: %w", name, err)
	}

	mu := e.lockFor(name)
	mu.Lock()
	defer mu.Unlock()

	targetName := name + fileExt
	if existing, err := e.root.ReadFile(targetName); err == nil && bytes.Equal(existing, payload) {
		return nil
	}
	if err := e.writeAtomic(name, payload); err != nil {
		return fmt.Errorf("yaml emitter: write %q: %w", targetName, err)
	}
	return nil
}

// Delete removes the file backing the named MCPServer. It is a no-op when no
// file exists for that name.
func (e *Emitter) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isSafeName(name) {
		return fmt.Errorf("yaml emitter: name %q is not a safe filename component", name)
	}

	mu := e.lockFor(name)
	mu.Lock()
	defer mu.Unlock()

	if err := e.root.Remove(name + fileExt); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("yaml emitter: remove %q: %w", name+fileExt, err)
	}
	_ = e.root.Remove(name + tempExt)
	return nil
}

func (e *Emitter) lockFor(name string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	mu, ok := e.locks[name]
	if !ok {
		mu = &sync.Mutex{}
		e.locks[name] = mu
	}
	return mu
}

func isSafeName(name string) bool {
	if name == "" || len(name) > maxNameLen {
		return false
	}
	return nameSafe.MatchString(name)
}

func nameFromModel(m translator.Model) (string, error) {
	if len(m.Backends) != 1 || len(m.Routes) != 1 || len(m.Policies) != 1 {
		return "", fmt.Errorf("model must declare exactly one backend, route and policy (got %d/%d/%d)",
			len(m.Backends), len(m.Routes), len(m.Policies))
	}
	name := m.Backends[0].Name
	if name == "" {
		return "", fmt.Errorf("backend name is empty")
	}
	if m.Routes[0].Name != name {
		return "", fmt.Errorf("route name %q does not match backend name %q", m.Routes[0].Name, name)
	}
	if m.Policies[0].Name != name {
		return "", fmt.Errorf("policy name %q does not match backend name %q", m.Policies[0].Name, name)
	}
	return name, nil
}

func buildLocalConfig(name string, m translator.Model, port uint16) (*configtypes.LocalConfig, error) {
	backend := m.Backends[0]
	route := m.Routes[0]
	policy := m.Policies[0]

	if backend.Host == "" {
		return nil, fmt.Errorf("backend %q has unresolved host", backend.Name)
	}
	if backend.Port <= 0 || backend.Port > math.MaxUint16 {
		return nil, fmt.Errorf("backend %q has out-of-range port %d", backend.Name, backend.Port)
	}

	target := configtypes.LocalMcpTarget{Name: backend.Name}
	endpoint := &configtypes.McpTargetEndpoint{
		Host: backend.Host,
		Port: uint16(backend.Port),
		Path: backend.Path,
	}
	switch backend.Protocol {
	case translator.ProtocolStreamableHTTP:
		target.MCP = endpoint
	case translator.ProtocolSSE:
		target.SSE = endpoint
	default:
		return nil, fmt.Errorf("unsupported backend protocol %q", backend.Protocol)
	}

	pathPrefix := route.PathMatch
	if pathPrefix == "" {
		pathPrefix = routePathRoot + name
	}

	emittedRoute := configtypes.LocalRoute{
		Name: route.Name,
		Matches: []configtypes.RouteMatch{
			{Path: &configtypes.PathMatch{PathPrefix: pathPrefix}},
		},
		Backends: []configtypes.LocalRouteBackend{
			{MCP: &configtypes.LocalMcpBackend{Targets: []configtypes.LocalMcpTarget{target}}},
		},
		Policies: policyFor(policy),
	}

	return &configtypes.LocalConfig{
		Binds: []configtypes.LocalBind{{
			Port: port,
			Listeners: []configtypes.LocalListener{{
				Name:   name,
				Routes: []configtypes.LocalRoute{emittedRoute},
			}},
		}},
	}, nil
}

func policyFor(p translator.Policy) *configtypes.FilterOrPolicy {
	if !p.Authn.ForwardToken {
		return nil
	}
	return &configtypes.FilterOrPolicy{
		BackendAuth: &configtypes.BackendAuth{Passthrough: &configtypes.Passthrough{}},
	}
}

func marshalConfig(cfg *configtypes.LocalConfig) ([]byte, error) {
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
func (e *Emitter) writeAtomic(name string, payload []byte) error {
	tmpName := name + tempExt
	targetName := name + fileExt

	f, err := e.root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePermission)
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = e.root.Remove(tmpName)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = e.root.Remove(tmpName)
		return err
	}
	if err := f.Close(); err != nil {
		_ = e.root.Remove(tmpName)
		return err
	}
	if err := e.root.Rename(tmpName, targetName); err != nil {
		_ = e.root.Remove(tmpName)
		return err
	}
	if dirFile, err := e.root.Open("."); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
