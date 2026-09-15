package testing

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	sigsyaml "sigs.k8s.io/yaml"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"gopkg.in/yaml.v3"
)

// Definition sources a scenario can run its muster serve instance on.
const (
	// ModeFilesystem is the default: definitions are files in the instance's
	// config directory, watched by the filesystem detector.
	ModeFilesystem = "filesystem"
	// ModeKubernetes runs the instance in Kubernetes mode against the run's
	// envtest API server: definitions are MCPServer and Workflow CRs in the
	// instance's own namespace, read through informers; the reconciler and the
	// boot pass drive the services the way they do on an installation.
	ModeKubernetes = "kubernetes"
)

// crdChartDir is the CRD chart whose CRDs the envtest API server gets, relative
// to the repository root.
var crdChartDir = filepath.Join("helm", "muster-crds", "files", "crds")

// instanceMode reports the definition source a pre-configuration asks for,
// defaulting to filesystem.
func instanceMode(config *MusterPreConfiguration) string {
	if config == nil || config.Mode == "" {
		return ModeFilesystem
	}
	return strings.ToLower(config.Mode)
}

// validateModeConfig rejects a mode block the harness cannot honour, at load
// time: an unknown mode, an apiserver block outside Kubernetes mode, and a
// stdio mock in Kubernetes mode, where muster refuses stdio definitions and
// the scenario would wait out its readiness for a server that never registers.
func validateModeConfig(config *MusterPreConfiguration) error {
	if config == nil {
		return nil
	}
	mode := instanceMode(config)
	switch mode {
	case ModeFilesystem, ModeKubernetes:
	default:
		return fmt.Errorf("pre_configuration.mode must be %q or %q, got %q", ModeFilesystem, ModeKubernetes, config.Mode)
	}
	if config.APIServer != nil {
		if mode != ModeKubernetes {
			return fmt.Errorf("pre_configuration.apiserver needs mode: %s", ModeKubernetes)
		}
		if config.APIServer.ReachableAfter < 0 {
			return fmt.Errorf("pre_configuration.apiserver.reachable_after must not be negative, got %s", config.APIServer.ReachableAfter)
		}
	}
	if mode != ModeKubernetes {
		return nil
	}
	for _, server := range config.MCPServers {
		serverType, _ := server.Config["type"].(string)
		if serverType == "" || serverType == "stdio" {
			return fmt.Errorf("mcp server %q: mode %s runs muster in Kubernetes mode, which refuses stdio servers; declare type: streamable-http or sse", server.Name, ModeKubernetes)
		}
	}
	return nil
}

// kubernetesModeUnavailableReason reports why Kubernetes-mode scenarios cannot
// run in this process, or "" when they can: the envtest control plane needs
// the kube-apiserver and etcd binaries KUBEBUILDER_ASSETS points at.
func kubernetesModeUnavailableReason() string {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		return "KUBEBUILDER_ASSETS is not set; mode: kubernetes scenarios need the envtest binaries (make test-envtest, or KUBEBUILDER_ASSETS=$(setup-envtest use -p path))"
	}
	for _, binary := range []string{"kube-apiserver", "etcd"} {
		// The directory comes from the environment on purpose: it is where
		// setup-envtest put the binaries, and only its presence is checked.
		if _, err := os.Stat(filepath.Join(assets, binary)); err != nil { //nolint:gosec
			return fmt.Sprintf("KUBEBUILDER_ASSETS=%s has no %s binary; mode: kubernetes scenarios need the envtest binaries", assets, binary)
		}
	}
	return ""
}

// findCRDDirectory locates the CRD chart's CRDs from the working directory
// upwards: muster test runs from the repository (make test, a checkout) and
// the API server needs the CRDs of the code under test, not a released copy.
func findCRDDirectory() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, crdChartDir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("no %s found between %s and the filesystem root; run muster test from a muster checkout", crdChartDir, cwd)
		}
	}
}

// envtestControlPlane is the one API server a muster test run shares between
// its Kubernetes-mode instances, started on the first of them. Each instance
// has its own namespace and its own proxy in front of it; the control plane
// itself is never touched by a scenario.
type envtestControlPlane struct {
	once     sync.Once
	env      *envtest.Environment
	config   *rest.Config
	client   client.Client
	startErr error
}

// start brings the control plane up once; later calls report the first
// result.
func (cp *envtestControlPlane) start(logger TestLogger, debug bool) error {
	cp.once.Do(func() {
		if reason := kubernetesModeUnavailableReason(); reason != "" {
			cp.startErr = errors.New(reason)
			return
		}
		crdDir, err := findCRDDirectory()
		if err != nil {
			cp.startErr = err
			return
		}
		env := &envtest.Environment{
			CRDDirectoryPaths:     []string{crdDir},
			ErrorIfCRDPathMissing: true,
		}
		cfg, err := env.Start()
		if err != nil {
			cp.startErr = fmt.Errorf("failed to start the envtest control plane: %w", err)
			return
		}
		scheme := runtime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
		c, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			_ = env.Stop()
			cp.startErr = fmt.Errorf("failed to create the envtest client: %w", err)
			return
		}
		cp.env, cp.config, cp.client = env, cfg, c
		if debug {
			logger.Debug("☸️  Started envtest control plane at %s with CRDs from %s\n", cfg.Host, crdDir)
		}
	})
	return cp.startErr
}

// apiServerAddr is the host:port the proxies dial (envtest reports the API
// server as a URL with a trailing slash).
func (cp *envtestControlPlane) apiServerAddr() string {
	if u, err := url.Parse(cp.config.Host); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(cp.config.Host, "https://"), "http://"), "/")
}

// stop tears the control plane down; a no-op when it never started.
func (cp *envtestControlPlane) stop() error {
	if cp.env == nil {
		return nil
	}
	err := cp.env.Stop()
	cp.env = nil
	return err
}

// renderKubeconfig writes the control plane's admin credentials into a
// kubeconfig whose server is the instance's proxy.
func renderKubeconfig(cfg *rest.Config, server string) ([]byte, error) {
	if len(cfg.CAData) == 0 {
		return nil, errors.New("envtest config carries no CA data")
	}
	user := map[string]interface{}{}
	switch {
	case cfg.BearerToken != "":
		user["token"] = cfg.BearerToken
	case len(cfg.CertData) > 0 && len(cfg.KeyData) > 0:
		user["client-certificate-data"] = base64.StdEncoding.EncodeToString(cfg.CertData)
		user["client-key-data"] = base64.StdEncoding.EncodeToString(cfg.KeyData)
	default:
		return nil, errors.New("envtest config carries neither a bearer token nor a client certificate")
	}
	kubeconfig := map[string]interface{}{
		keyAPIVersion: "v1",
		"kind":        "Config",
		"clusters": []interface{}{map[string]interface{}{
			"name": "envtest",
			"cluster": map[string]interface{}{
				"server":                     server,
				"certificate-authority-data": base64.StdEncoding.EncodeToString(cfg.CAData),
			},
		}},
		"users": []interface{}{map[string]interface{}{
			"name": "muster",
			"user": user,
		}},
		"contexts": []interface{}{map[string]interface{}{
			"name":    "envtest",
			"context": map[string]interface{}{"cluster": "envtest", "user": "muster"},
		}},
		"current-context": "envtest",
	}
	return yaml.Marshal(kubeconfig)
}

// instanceNamespace derives the instance's namespace from its ID: a DNS label
// (lower case, 63 characters at most) that keeps the scenario name readable
// and stays unique through a hash of the full ID.
func instanceNamespace(instanceID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(instanceID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	sum := sha256.Sum256([]byte(instanceID))
	suffix := hex.EncodeToString(sum[:])[:8]
	const maxLabel = 63
	label := strings.Trim(b.String(), "-")
	if label == "" {
		label = "instance"
	}
	if len(label) > maxLabel-len(suffix)-1 {
		label = strings.TrimRight(label[:maxLabel-len(suffix)-1], "-")
	}
	return label + "-" + suffix
}

// instanceKubernetes is what a Kubernetes-mode instance owns on the shared
// control plane: its namespace, the kubeconfig muster serve was given and the
// proxy that kubeconfig points at.
type instanceKubernetes struct {
	namespace      string
	kubeconfigPath string
	proxy          *apiServerProxy
	proxyPort      int
}

// startKubernetes prepares a Kubernetes-mode instance: the run's control
// plane (started on first use), the instance's namespace, its API server
// proxy on a port from the harness's allocator, and the kubeconfig muster
// serve reads through KUBECONFIG. With apiserver.reachable_after the proxy
// opens that long after this returns, which is after muster serve started.
func (m *musterInstanceManager) startKubernetes(ctx context.Context, instanceID, configPath string, config *MusterPreConfiguration, logger TestLogger) error {
	if instanceMode(config) != ModeKubernetes {
		return nil
	}
	if err := m.envtest.start(logger, m.debug); err != nil {
		return fmt.Errorf("mode %s: %w", ModeKubernetes, err)
	}

	port, err := m.findAvailablePort(instanceID, logger)
	if err != nil {
		return fmt.Errorf("failed to find available port for the api server proxy: %w", err)
	}
	m.closeReservedListener(port)

	ik := &instanceKubernetes{
		namespace:      instanceNamespace(instanceID),
		kubeconfigPath: filepath.Join(configPath, "kubeconfig"),
		proxy:          newAPIServerProxy(fmt.Sprintf("127.0.0.1:%d", port), m.envtest.apiServerAddr()),
		proxyPort:      port,
	}
	m.mu.Lock()
	m.kubernetes[instanceID] = ik
	m.mu.Unlock()

	fail := func(err error) error {
		m.stopKubernetes(instanceID, logger)
		return err
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ik.namespace}}
	if err := m.envtest.client.Create(ctx, ns); err != nil {
		return fail(fmt.Errorf("failed to create namespace %s: %w", ik.namespace, err))
	}
	kubeconfig, err := renderKubeconfig(m.envtest.config, "https://"+ik.proxy.addr())
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(ik.kubeconfigPath, kubeconfig, 0o600); err != nil {
		return fail(fmt.Errorf("failed to write kubeconfig: %w", err))
	}

	var delay time.Duration
	if config.APIServer != nil {
		delay = config.APIServer.ReachableAfter
	}
	if delay <= 0 {
		if err := ik.proxy.open(); err != nil {
			return fail(err)
		}
		if m.debug {
			logger.Debug("☸️  Instance %s runs in Kubernetes mode: namespace %s, api server via %s\n", instanceID, ik.namespace, ik.proxy.addr())
		}
		return nil
	}
	if m.debug {
		logger.Debug("☸️  Instance %s runs in Kubernetes mode: namespace %s, api server via %s reachable after %s\n", instanceID, ik.namespace, ik.proxy.addr(), delay)
	}
	ik.proxy.openAfter(ctx, delay, func(err error) {
		if err != nil {
			logger.Debug("⚠️  Delayed api server open for %s failed: %v\n", instanceID, err)
		} else if m.debug {
			logger.Debug("☸️  Api server for %s now reachable via %s\n", instanceID, ik.proxy.addr())
		}
	})
	return nil
}

// kubernetesFor returns the instance's Kubernetes-mode state, or nil when the
// instance runs on the filesystem.
func (m *musterInstanceManager) kubernetesFor(instanceID string) *instanceKubernetes {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kubernetes[instanceID]
}

// definitionsRoot is the directory the instance's MCPServer and Workflow
// definitions are rendered into: muster's own config directory in filesystem
// mode, where it reads them; crs/ beside it in Kubernetes mode, where the
// harness applies them from and muster never looks.
func (m *musterInstanceManager) definitionsRoot(configPath, instanceID string) string {
	if m.kubernetesFor(instanceID) != nil {
		return filepath.Join(configPath, "crs")
	}
	return filepath.Join(configPath, "muster")
}

// applyModeConfig switches muster's main configuration to Kubernetes mode
// in the instance's namespace. A filesystem instance leaves it alone.
func (m *musterInstanceManager) applyModeConfig(mainConfig map[string]interface{}, instanceID string) {
	ik := m.kubernetesFor(instanceID)
	if ik == nil {
		return
	}
	mainConfig["kubernetes"] = true
	mainConfig["namespace"] = ik.namespace
}

// applyDefinitions creates the definitions the harness rendered into
// definitionsRoot -- the same MCPServer and Workflow documents a filesystem
// instance reads from its config directory -- as CRs in the instance's
// namespace. Straight to the control plane, not through the proxy, so a
// scenario whose API server is unreachable at start still finds its CRs there
// once it is reachable.
func (m *musterInstanceManager) applyDefinitions(ctx context.Context, instanceID, definitionsRoot string, logger TestLogger) error {
	ik := m.kubernetesFor(instanceID)
	if ik == nil {
		return nil
	}
	for _, kind := range []string{"mcpservers", "workflows"} {
		files, err := filepath.Glob(filepath.Join(definitionsRoot, kind, "*.yaml"))
		if err != nil {
			return err
		}
		for _, file := range files {
			data, err := os.ReadFile(file) //nolint:gosec
			if err != nil {
				return fmt.Errorf("failed to read definition %s: %w", file, err)
			}
			obj := &unstructured.Unstructured{}
			if err := sigsyaml.Unmarshal(data, &obj.Object); err != nil {
				return fmt.Errorf("failed to parse definition %s: %w", file, err)
			}
			obj.SetNamespace(ik.namespace)
			if err := m.envtest.client.Create(ctx, obj); err != nil {
				return fmt.Errorf("failed to create %s %s in namespace %s: %w", obj.GetKind(), obj.GetName(), ik.namespace, err)
			}
			if m.debug {
				logger.Debug("☸️  Applied %s %s to namespace %s\n", obj.GetKind(), obj.GetName(), ik.namespace)
			}
		}
	}
	return nil
}

// stopKubernetes releases what the instance held on the control plane: the
// proxy and its port, the CRs of its namespace and the namespace itself
// (envtest runs no namespace controller, so the namespace stays terminating
// -- empty, and never reused since every instance ID is unique).
func (m *musterInstanceManager) stopKubernetes(instanceID string, logger TestLogger) {
	m.mu.Lock()
	ik, exists := m.kubernetes[instanceID]
	if exists {
		delete(m.kubernetes, instanceID)
	}
	m.mu.Unlock()
	if !exists {
		return
	}
	ik.proxy.shutdown()
	m.releasePort(ik.proxyPort, instanceID, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inNamespace := client.InNamespace(ik.namespace)
	for _, obj := range []client.Object{&musterv1alpha1.MCPServer{}, &musterv1alpha1.Workflow{}} {
		if err := m.envtest.client.DeleteAllOf(ctx, obj, inNamespace); err != nil && !apierrors.IsNotFound(err) {
			logger.Debug("⚠️  Failed to delete the CRs of namespace %s: %v\n", ik.namespace, err)
		}
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ik.namespace}}
	if err := m.envtest.client.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		logger.Debug("⚠️  Failed to delete namespace %s: %v\n", ik.namespace, err)
	}
	if m.debug {
		logger.Debug("☸️  Released Kubernetes-mode state of %s (namespace %s)\n", instanceID, ik.namespace)
	}
}

// requireKubernetes returns the instance's Kubernetes-mode state or the error
// a filesystem-mode instance gets from a Kubernetes-only tool.
func (m *musterInstanceManager) requireKubernetes(instanceID string) (*instanceKubernetes, error) {
	ik := m.kubernetesFor(instanceID)
	if ik == nil {
		return nil, fmt.Errorf("instance %s runs in %s mode; declare pre_configuration.mode: %s", instanceID, ModeFilesystem, ModeKubernetes)
	}
	return ik, nil
}

// SetAPIServerReachable closes or opens the instance's API server proxy.
func (m *musterInstanceManager) SetAPIServerReachable(instanceID string, reachable bool) error {
	ik, err := m.requireKubernetes(instanceID)
	if err != nil {
		return err
	}
	if reachable {
		return ik.proxy.open()
	}
	ik.proxy.close()
	return nil
}

// crObject returns an empty unstructured object for a muster CR kind. The
// kind is matched without regard to case, so scenarios may write mcpserver.
func crObject(kind string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	switch strings.ToLower(kind) {
	case "mcpserver", "":
		obj.SetGroupVersionKind(musterv1alpha1.GroupVersion.WithKind("MCPServer"))
	case "workflow":
		obj.SetGroupVersionKind(musterv1alpha1.GroupVersion.WithKind("Workflow"))
	default:
		return nil, fmt.Errorf("kind must be MCPServer or Workflow, got %q", kind)
	}
	return obj, nil
}

// PatchCR applies a JSON merge patch (RFC 7386: nested objects merge, null
// removes) to a CR of the instance's namespace and returns the stored object.
func (m *musterInstanceManager) PatchCR(ctx context.Context, instanceID, kind, name string, patch map[string]interface{}) (map[string]interface{}, error) {
	ik, err := m.requireKubernetes(instanceID)
	if err != nil {
		return nil, err
	}
	obj, err := crObject(kind)
	if err != nil {
		return nil, err
	}
	obj.SetName(name)
	obj.SetNamespace(ik.namespace)
	data, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to render patch: %w", err)
	}
	if err := m.envtest.client.Patch(ctx, obj, client.RawPatch(types.MergePatchType, data)); err != nil {
		return nil, fmt.Errorf("failed to patch %s %s: %w", obj.GetKind(), name, err)
	}
	return obj.Object, nil
}

// GetCR reads a CR of the instance's namespace as the API server stores it.
func (m *musterInstanceManager) GetCR(ctx context.Context, instanceID, kind, name string) (map[string]interface{}, error) {
	ik, err := m.requireKubernetes(instanceID)
	if err != nil {
		return nil, err
	}
	obj, err := crObject(kind)
	if err != nil {
		return nil, err
	}
	key := client.ObjectKey{Namespace: ik.namespace, Name: name}
	if err := m.envtest.client.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("failed to get %s %s: %w", obj.GetKind(), name, err)
	}
	return obj.Object, nil
}

// MutateMCPServerDefinition applies mutate to an MCPServer definition of the
// instance where muster reads it: the CR in Kubernetes mode (read, mutate,
// update), the file in the instance's config directory in filesystem mode.
// Either way the change reaches muster as a definition update -- informer
// event or filesystem event -- and the reconciler acts on it.
func (m *musterInstanceManager) MutateMCPServerDefinition(ctx context.Context, instance *MusterInstance, name string, mutate func(definition map[string]interface{}) error) error {
	if ik := m.kubernetesFor(instance.ID); ik != nil {
		obj, err := crObject("MCPServer")
		if err != nil {
			return err
		}
		key := client.ObjectKey{Namespace: ik.namespace, Name: name}
		if err := m.envtest.client.Get(ctx, key, obj); err != nil {
			return fmt.Errorf("failed to get MCPServer %s: %w", name, err)
		}
		if err := mutate(obj.Object); err != nil {
			return err
		}
		if err := m.envtest.client.Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to update MCPServer %s: %w", name, err)
		}
		return nil
	}

	filename := filepath.Join(instance.ConfigPath, "muster", "mcpservers", name+".yaml")
	data, err := os.ReadFile(filename) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to read MCPServer definition %s: %w", filename, err)
	}
	var definition map[string]interface{}
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return fmt.Errorf("failed to parse MCPServer definition %s: %w", filename, err)
	}
	if definition == nil {
		definition = map[string]interface{}{}
	}
	if err := mutate(definition); err != nil {
		return err
	}
	out, err := yaml.Marshal(definition)
	if err != nil {
		return fmt.Errorf("failed to render MCPServer definition %s: %w", filename, err)
	}
	if err := os.WriteFile(filename, out, 0o600); err != nil {
		return fmt.Errorf("failed to write MCPServer definition %s: %w", filename, err)
	}
	return nil
}
