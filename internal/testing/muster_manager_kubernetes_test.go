package testing

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"gopkg.in/yaml.v3"
)

func TestInstanceMode(t *testing.T) {
	require.Equal(t, ModeFilesystem, instanceMode(nil))
	require.Equal(t, ModeFilesystem, instanceMode(&MusterPreConfiguration{}))
	require.Equal(t, ModeKubernetes, instanceMode(&MusterPreConfiguration{Mode: "Kubernetes"}))
}

func TestValidateModeConfig(t *testing.T) {
	remote := []MCPServerConfig{{Name: "r", Config: map[string]interface{}{"type": "streamable-http"}}}
	stdio := []MCPServerConfig{{Name: "s", Config: map[string]interface{}{"tools": []interface{}{}}}}
	cases := []struct {
		name    string
		cfg     *MusterPreConfiguration
		wantErr string
	}{
		{"nil pre-configuration", nil, ""},
		{"no mode", &MusterPreConfiguration{MCPServers: stdio}, ""},
		{"filesystem", &MusterPreConfiguration{Mode: "filesystem", MCPServers: stdio}, ""},
		{"kubernetes with remote mocks", &MusterPreConfiguration{Mode: "kubernetes", MCPServers: remote}, ""},
		{"kubernetes with apiserver delay", &MusterPreConfiguration{Mode: "kubernetes", APIServer: &APIServerConfig{ReachableAfter: time.Second}}, ""},
		{"unknown mode", &MusterPreConfiguration{Mode: "etcd"}, "pre_configuration.mode must be"},
		{"apiserver block on filesystem", &MusterPreConfiguration{APIServer: &APIServerConfig{}}, "needs mode: kubernetes"},
		{"negative delay", &MusterPreConfiguration{Mode: "kubernetes", APIServer: &APIServerConfig{ReachableAfter: -time.Second}}, "must not be negative"},
		{"stdio mock in kubernetes mode", &MusterPreConfiguration{Mode: "kubernetes", MCPServers: stdio}, "refuses stdio servers"},
		{"explicit stdio in kubernetes mode", &MusterPreConfiguration{Mode: "kubernetes", MCPServers: []MCPServerConfig{{Name: "s", Config: map[string]interface{}{"type": "stdio"}}}}, "refuses stdio servers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateModeConfig(tc.cfg)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestKubernetesModeUnavailableReason(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "")
	require.Contains(t, kubernetesModeUnavailableReason(), "KUBEBUILDER_ASSETS is not set")

	empty := t.TempDir()
	t.Setenv("KUBEBUILDER_ASSETS", empty)
	require.Contains(t, kubernetesModeUnavailableReason(), "has no kube-apiserver binary")

	require.NoError(t, os.WriteFile(filepath.Join(empty, "kube-apiserver"), []byte("#!/bin/sh\n"), 0o700)) //nolint:gosec
	require.Contains(t, kubernetesModeUnavailableReason(), "has no etcd binary")

	require.NoError(t, os.WriteFile(filepath.Join(empty, "etcd"), []byte("#!/bin/sh\n"), 0o700)) //nolint:gosec
	require.Empty(t, kubernetesModeUnavailableReason())
}

func TestModeUnavailableReasonOnlyForKubernetesMode(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "")
	require.Empty(t, modeUnavailableReason(TestScenario{}))
	require.Empty(t, modeUnavailableReason(TestScenario{PreConfiguration: &MusterPreConfiguration{Mode: "filesystem"}}))
	require.NotEmpty(t, modeUnavailableReason(TestScenario{PreConfiguration: &MusterPreConfiguration{Mode: "kubernetes"}}))
}

func TestInstanceNamespaceIsADNSLabel(t *testing.T) {
	label := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	for _, id := range []string{
		"test-mcpserver-kubernetes-mode-boots-from-crs-1789469236912280802",
		"test-Under_Score.Dots-1",
		strings.Repeat("test-very-long-scenario-name-", 5) + "-1789469236912280802",
		"---",
	} {
		ns := instanceNamespace(id)
		require.LessOrEqual(t, len(ns), 63, "%q -> %q", id, ns)
		require.Regexp(t, label, ns, "%q -> %q", id, ns)
		require.Equal(t, ns, instanceNamespace(id), "deterministic")
	}
	require.NotEqual(t, instanceNamespace("test-a-1"), instanceNamespace("test-a-2"), "distinct IDs get distinct namespaces")
	require.True(t, strings.HasPrefix(instanceNamespace("test-mcpserver-suspend-once-1"), "test-mcpserver-suspend-once-1-"), "the readable part is kept")
}

func TestRenderKubeconfigPointsAtTheProxy(t *testing.T) {
	cfg := &rest.Config{
		Host: "https://127.0.0.1:45289/",
		TLSClientConfig: rest.TLSClientConfig{
			CAData:   []byte("ca"),
			CertData: []byte("cert"),
			KeyData:  []byte("key"),
		},
	}
	data, err := renderKubeconfig(cfg, "https://127.0.0.1:32004")
	require.NoError(t, err)

	var kubeconfig map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &kubeconfig))
	clusters := kubeconfig["clusters"].([]interface{})
	cluster := clusters[0].(map[string]interface{})["cluster"].(map[string]interface{})
	require.Equal(t, "https://127.0.0.1:32004", cluster["server"], "the server is the proxy, not the control plane")
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("ca")), cluster["certificate-authority-data"])
	user := kubeconfig["users"].([]interface{})[0].(map[string]interface{})["user"].(map[string]interface{})
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("cert")), user["client-certificate-data"])
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("key")), user["client-key-data"])
	require.Equal(t, "envtest", kubeconfig["current-context"])

	token := &rest.Config{Host: cfg.Host, BearerToken: "t0k3n", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}
	data, err = renderKubeconfig(token, "https://127.0.0.1:1")
	require.NoError(t, err)
	require.Contains(t, string(data), "token: t0k3n")

	_, err = renderKubeconfig(&rest.Config{Host: cfg.Host}, "https://127.0.0.1:1")
	require.ErrorContains(t, err, "no CA data")
	_, err = renderKubeconfig(&rest.Config{Host: cfg.Host, TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}, "https://127.0.0.1:1")
	require.ErrorContains(t, err, "neither a bearer token nor a client certificate")
}

func TestCRObjectKinds(t *testing.T) {
	for kind, want := range map[string]string{"": "MCPServer", "MCPServer": "MCPServer", "mcpserver": "MCPServer", "Workflow": "Workflow", "workflow": "Workflow"} {
		obj, err := crObject(kind)
		require.NoError(t, err, kind)
		require.Equal(t, want, obj.GetKind(), kind)
		require.Equal(t, musterv1alpha1.GroupVersion.String(), obj.GetAPIVersion())
	}
	_, err := crObject("Pod")
	require.ErrorContains(t, err, "kind must be MCPServer or Workflow")
}

func TestKubernetesToolsRefuseAFilesystemInstance(t *testing.T) {
	m := newStorageTestManager(t)
	ctx := context.Background()
	require.Nil(t, m.kubernetesFor("inst"))
	require.ErrorContains(t, m.SetAPIServerReachable("inst", false), "declare pre_configuration.mode: kubernetes")
	_, err := m.PatchCR(ctx, "inst", "MCPServer", "x", map[string]interface{}{"spec": map[string]interface{}{"suspended": true}})
	require.ErrorContains(t, err, "declare pre_configuration.mode: kubernetes")
	_, err = m.GetCR(ctx, "inst", "MCPServer", "x")
	require.ErrorContains(t, err, "declare pre_configuration.mode: kubernetes")

	// A filesystem instance's main config is left alone.
	mainConfig := map[string]interface{}{"aggregator": map[string]interface{}{}}
	m.applyModeConfig(mainConfig, "inst")
	require.NotContains(t, mainConfig, "kubernetes")
	require.NoError(t, m.startKubernetes(ctx, "inst", t.TempDir(), &MusterPreConfiguration{}, m.logger), "filesystem mode starts nothing")
	require.NoError(t, m.applyDefinitions(ctx, "inst", t.TempDir(), m.logger), "filesystem mode applies nothing")
}

func TestStartKubernetesWithoutAssetsFailsNamingTheReason(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "")
	m := newStorageTestManager(t)
	err := m.startKubernetes(context.Background(), "inst", t.TempDir(), &MusterPreConfiguration{Mode: ModeKubernetes}, m.logger)
	require.ErrorContains(t, err, "KUBEBUILDER_ASSETS is not set")
	require.Nil(t, m.kubernetesFor("inst"), "nothing is left behind")
}

func TestMutateMCPServerDefinitionOnTheFilesystem(t *testing.T) {
	m := newStorageTestManager(t)
	configPath := t.TempDir()
	dir := filepath.Join(configPath, "muster", "mcpservers")
	require.NoError(t, os.MkdirAll(dir, 0o755)) //nolint:gosec
	definition := map[string]interface{}{
		"apiVersion": "muster.giantswarm.io/v1alpha1",
		"kind":       "MCPServer",
		"metadata":   map[string]interface{}{"name": "srv", "namespace": "default"},
		"spec":       map[string]interface{}{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp"},
	}
	data, err := yaml.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "srv.yaml"), data, 0o600))

	instance := &MusterInstance{ID: "inst", ConfigPath: configPath}
	err = m.MutateMCPServerDefinition(context.Background(), instance, "srv", func(def map[string]interface{}) error {
		def["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"tier": "gold"}
		return nil
	})
	require.NoError(t, err)

	written, err := os.ReadFile(filepath.Join(dir, "srv.yaml"))
	require.NoError(t, err)
	var got map[string]interface{}
	require.NoError(t, yaml.Unmarshal(written, &got))
	require.Equal(t, "gold", got["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["tier"])
	require.Equal(t, "http://127.0.0.1:1/mcp", got["spec"].(map[string]interface{})["url"], "the rest of the definition is kept")

	err = m.MutateMCPServerDefinition(context.Background(), instance, "missing", func(map[string]interface{}) error { return nil })
	require.ErrorContains(t, err, "failed to read MCPServer definition")
}

// TestKubernetesModeEnvtest runs the Kubernetes-mode plumbing against a real
// envtest control plane: namespace and kubeconfig per instance, definitions
// applied as CRs, patch, get, the mode-aware mutation and the proxy's cut and
// restore. Skipped without KUBEBUILDER_ASSETS (run via make test-envtest).
func TestKubernetesModeEnvtest(t *testing.T) {
	if reason := kubernetesModeUnavailableReason(); reason != "" {
		t.Skip(reason)
	}
	m := newStorageTestManager(t)
	ctx := context.Background()
	configPath := t.TempDir()
	const instanceID = "test-kubernetes-mode-envtest-1"
	require.Equal(t, filepath.Join(configPath, "muster"), m.definitionsRoot(configPath, instanceID), "filesystem mode renders into muster's config directory")
	require.NoError(t, m.startKubernetes(ctx, instanceID, configPath, &MusterPreConfiguration{Mode: ModeKubernetes}, m.logger))
	definitionsRoot := m.definitionsRoot(configPath, instanceID)
	require.Equal(t, filepath.Join(configPath, "crs"), definitionsRoot, "Kubernetes mode renders beside it, where muster never reads")
	require.NoError(t, os.MkdirAll(filepath.Join(definitionsRoot, "mcpservers"), 0o755)) //nolint:gosec
	definition := map[string]interface{}{
		"apiVersion": "muster.giantswarm.io/v1alpha1",
		"kind":       "MCPServer",
		"metadata":   map[string]interface{}{"name": "srv", "namespace": "default"},
		"spec":       map[string]interface{}{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp", "autoStart": true},
	}
	data, err := yaml.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(definitionsRoot, "mcpservers", "srv.yaml"), data, 0o600))
	t.Cleanup(func() { m.stopKubernetes(instanceID, m.logger) })
	ik := m.kubernetesFor(instanceID)
	require.NotNil(t, ik)
	require.True(t, ik.proxy.reachable(), "reachable from the start without reachable_after")
	require.FileExists(t, ik.kubeconfigPath)

	mainConfig := map[string]interface{}{}
	m.applyModeConfig(mainConfig, instanceID)
	require.Equal(t, true, mainConfig["kubernetes"])
	require.Equal(t, ik.namespace, mainConfig["namespace"])

	require.NoError(t, m.applyDefinitions(ctx, instanceID, definitionsRoot, m.logger))
	got, err := m.GetCR(ctx, instanceID, "", "srv")
	require.NoError(t, err)
	require.Equal(t, ik.namespace, got["metadata"].(map[string]interface{})["namespace"], "the CR lives in the instance's namespace, not in default")

	patched, err := m.PatchCR(ctx, instanceID, "mcpserver", "srv", map[string]interface{}{"spec": map[string]interface{}{"suspended": true}})
	require.NoError(t, err)
	require.Equal(t, true, patched["spec"].(map[string]interface{})["suspended"])
	require.Equal(t, "http://127.0.0.1:1/mcp", patched["spec"].(map[string]interface{})["url"], "a merge patch keeps the other fields")

	instance := &MusterInstance{ID: instanceID, ConfigPath: configPath}
	require.NoError(t, m.MutateMCPServerDefinition(ctx, instance, "srv", func(def map[string]interface{}) error {
		def["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"tier": "gold"}
		return nil
	}))
	got, err = m.GetCR(ctx, instanceID, "MCPServer", "srv")
	require.NoError(t, err)
	require.Equal(t, "gold", got["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["tier"])
	require.Equal(t, true, got["spec"].(map[string]interface{})["suspended"], "the mutation went through the live CR, the patch is kept")

	_, err = m.GetCR(ctx, instanceID, "Workflow", "srv")
	require.Error(t, err, "no Workflow of that name")

	// The kubeconfig reaches the control plane through the proxy, and only
	// while the proxy is open.
	viaProxy, err := client.New(mustRESTConfigFromKubeconfig(t, ik.kubeconfigPath), client.Options{Scheme: m.envtest.client.Scheme()})
	require.NoError(t, err)
	var cr musterv1alpha1.MCPServer
	require.NoError(t, viaProxy.Get(ctx, client.ObjectKey{Namespace: ik.namespace, Name: "srv"}, &cr))
	require.True(t, cr.Spec.Suspended)

	require.NoError(t, m.SetAPIServerReachable(instanceID, false))
	shortCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	require.Error(t, viaProxy.Get(shortCtx, client.ObjectKey{Namespace: ik.namespace, Name: "srv"}, &cr), "a closed proxy refuses the request")
	require.NoError(t, m.SetAPIServerReachable(instanceID, true))
	require.NoError(t, viaProxy.Get(ctx, client.ObjectKey{Namespace: ik.namespace, Name: "srv"}, &cr), "and answers again once open")

	m.stopKubernetes(instanceID, m.logger)
	require.Nil(t, m.kubernetesFor(instanceID))
	err = m.envtest.client.Get(ctx, client.ObjectKey{Namespace: ik.namespace, Name: "srv"}, &cr)
	require.Error(t, err, "the CRs of a destroyed instance are gone from the shared control plane")
}

// mustRESTConfigFromKubeconfig builds a rest.Config from the kubeconfig the
// harness wrote, the way muster's config detection reads it -- without pulling
// clientcmd into the test binary.
func mustRESTConfigFromKubeconfig(t *testing.T, path string) *rest.Config {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	var kubeconfig struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
				CA     string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			User struct {
				Cert string `yaml:"client-certificate-data"`
				Key  string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	require.NoError(t, yaml.Unmarshal(data, &kubeconfig))
	require.Len(t, kubeconfig.Clusters, 1)
	require.Len(t, kubeconfig.Users, 1)
	decode := func(s string) []byte {
		out, err := base64.StdEncoding.DecodeString(s)
		require.NoError(t, err)
		return out
	}
	return &rest.Config{
		Host: kubeconfig.Clusters[0].Cluster.Server,
		TLSClientConfig: rest.TLSClientConfig{
			CAData:   decode(kubeconfig.Clusters[0].Cluster.CA),
			CertData: decode(kubeconfig.Users[0].User.Cert),
			KeyData:  decode(kubeconfig.Users[0].User.Key),
		},
	}
}
