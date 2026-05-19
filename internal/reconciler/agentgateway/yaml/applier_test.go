package yaml_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	goyaml "gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	yamlapply "github.com/giantswarm/muster/internal/reconciler/agentgateway/yaml"
)

func readInDir(t *testing.T, dir, name string) []byte {
	t.Helper()
	f, err := os.OpenInRoot(dir, name)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	return data
}

func writeInDir(t *testing.T, dir, name string, payload []byte) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	require.NoError(t, root.WriteFile(name, payload, 0o600))
}

const (
	fixtureAlpha   = "alpha"
	fixtureBravo   = "bravo"
	fixtureGrizzly = "grizzly"
)

func canonicalConfig() agentgateway.Config {
	return agentgateway.Config{
		Name:      fixtureGrizzly,
		Namespace: "muster",
		Backends: []agentgateway.Backend{{
			Name: fixtureGrizzly,
			Target: agentgateway.HTTPTarget{
				Protocol: agentgateway.StreamableHTTP,
				Host:     "grizzly.muster.svc.cluster.local",
				Port:     8443,
				Path:     "/mcp",
			},
		}},
		Routes: []agentgateway.Route{{
			Name:       fixtureGrizzly,
			PathMatch:  "/mcp/grizzly",
			BackendRef: fixtureGrizzly,
			PolicyRef:  fixtureGrizzly,
		}},
		Policies: []agentgateway.Policy{{
			Name:  fixtureGrizzly,
			Authn: agentgateway.Authn{Type: agentgateway.AuthnTypeOAuth, ForwardToken: true},
		}},
	}
}

func sseConfig() agentgateway.Config {
	c := canonicalConfig()
	c.Backends[0].Target = agentgateway.HTTPTarget{
		Protocol: agentgateway.SSE,
		Host:     "grizzly.muster.svc.cluster.local",
		Port:     8443,
		Path:     "/sse",
	}
	c.Policies[0].Authn = agentgateway.Authn{Type: agentgateway.AuthnTypeNone}
	return c
}

func stdioConfig() agentgateway.Config {
	c := canonicalConfig()
	c.Backends[0].Target = agentgateway.StdioTarget{
		Command: "/usr/local/bin/mcp-kubernetes",
		Args:    []string{"--workdir", "/data"},
		Env:     map[string]string{"LOG_LEVEL": "info"},
	}
	c.Policies[0].Authn = agentgateway.Authn{Type: agentgateway.AuthnTypeNone}
	return c
}

func TestApply_WritesCombinedFile(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	data := readInDir(t, dir, yamlapply.ConfigFilename)

	first := strings.SplitN(string(data), "\n", 2)[0]
	require.Equal(t, "# yaml-language-server: $schema="+yamlapply.SchemaURL, first)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "applier must write a single combined file")
	require.Equal(t, yamlapply.ConfigFilename, entries[0].Name())
}

func TestApply_ReApplyIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))
	first := readInDir(t, dir, yamlapply.ConfigFilename)

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))
	second := readInDir(t, dir, yamlapply.ConfigFilename)

	require.True(t, bytes.Equal(first, second), "re-apply must produce byte-identical output")
}

func TestApply_RoundTripUnmarshal(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))
	raw := readInDir(t, dir, yamlapply.ConfigFilename)

	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))

	require.Len(t, cfg.Binds, 1)
	require.Equal(t, yamlapply.DefaultListenerPort, cfg.Binds[0].Port)
	require.Len(t, cfg.Binds[0].Listeners, 1)

	listener := cfg.Binds[0].Listeners[0]
	require.Equal(t, yamlapply.DefaultListenerName, listener.Name)
	require.Len(t, listener.Routes, 1)

	route := listener.Routes[0]
	require.Equal(t, fixtureGrizzly, route.Name)
	require.Len(t, route.Matches, 1)
	require.NotNil(t, route.Matches[0].Path)
	require.Equal(t, "/mcp/grizzly", route.Matches[0].Path.PathPrefix)

	require.Len(t, route.Backends, 1)
	require.NotNil(t, route.Backends[0].MCP)
	require.Len(t, route.Backends[0].MCP.Targets, 1)

	target := route.Backends[0].MCP.Targets[0]
	require.Equal(t, fixtureGrizzly, target.Name)
	require.NotNil(t, target.MCP)
	require.Nil(t, target.SSE)
	require.Nil(t, target.Stdio)
	require.Equal(t, "grizzly.muster.svc.cluster.local", target.MCP.Host)
	require.Equal(t, uint16(8443), target.MCP.Port)
	require.Equal(t, "/mcp", target.MCP.Path)

	require.NotNil(t, route.Policies)
	require.NotNil(t, route.Policies.BackendAuth)
	require.NotNil(t, route.Policies.BackendAuth.Passthrough)
}

func TestApply_SSEProtocol(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), sseConfig()))
	raw := readInDir(t, dir, yamlapply.ConfigFilename)

	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	target := cfg.Binds[0].Listeners[0].Routes[0].Backends[0].MCP.Targets[0]
	require.Nil(t, target.MCP)
	require.NotNil(t, target.SSE)
	require.Equal(t, "/sse", target.SSE.Path)

	require.Nil(t, cfg.Binds[0].Listeners[0].Routes[0].Policies,
		"AuthnTypeNone with no ForwardToken must omit the policies block")
}

func TestApply_StdioTarget(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), stdioConfig()))
	raw := readInDir(t, dir, yamlapply.ConfigFilename)

	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	target := cfg.Binds[0].Listeners[0].Routes[0].Backends[0].MCP.Targets[0]
	require.Nil(t, target.MCP, "stdio target must not write an mcp endpoint")
	require.Nil(t, target.SSE, "stdio target must not write an sse endpoint")
	require.NotNil(t, target.Stdio)
	require.Equal(t, "/usr/local/bin/mcp-kubernetes", target.Stdio.Cmd)
	require.Equal(t, []string{"--workdir", "/data"}, target.Stdio.Args)
	require.Equal(t, map[string]string{"LOG_LEVEL": "info"}, target.Stdio.Env)
}

func TestApply_CustomListenerPort(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir, yamlapply.WithListenerPort(9090))
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	raw := readInDir(t, dir, yamlapply.ConfigFilename)

	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	require.Equal(t, uint16(9090), cfg.Binds[0].Port)
}

func TestApply_CustomListenerName(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir, yamlapply.WithListenerName("alt"))
	require.NoError(t, err)

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	raw := readInDir(t, dir, yamlapply.ConfigFilename)

	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	require.Equal(t, "alt", cfg.Binds[0].Listeners[0].Name)
}

func TestApply_MultipleMCPServersShareOneListener(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	a1 := canonicalConfig()
	a1.Backends[0].Name = fixtureAlpha
	a1.Routes[0].Name = fixtureAlpha
	a1.Routes[0].PathMatch = "/mcp/alpha"
	a1.Policies[0].Name = fixtureAlpha

	a2 := stdioConfig()
	a2.Backends[0].Name = fixtureBravo
	a2.Routes[0].Name = fixtureBravo
	a2.Routes[0].PathMatch = "/mcp/bravo"
	a2.Policies[0].Name = fixtureBravo

	require.NoError(t, a.Apply(t.Context(), a1))
	require.NoError(t, a.Apply(t.Context(), a2))

	raw := readInDir(t, dir, yamlapply.ConfigFilename)

	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))

	require.Len(t, cfg.Binds, 1, "all routes must share one bind")
	require.Len(t, cfg.Binds[0].Listeners, 1, "all routes must share one listener")
	listener := cfg.Binds[0].Listeners[0]
	require.Equal(t, yamlapply.DefaultListenerName, listener.Name)
	require.Len(t, listener.Routes, 2)

	require.Equal(t, fixtureAlpha, listener.Routes[0].Name, "routes must sort by name for deterministic output")
	require.Equal(t, fixtureBravo, listener.Routes[1].Name)

	require.NotNil(t, listener.Routes[0].Backends[0].MCP.Targets[0].MCP)
	require.NotNil(t, listener.Routes[1].Backends[0].MCP.Targets[0].Stdio)
}

func TestApply_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	bootstrapBefore := readInDir(t, dir, yamlapply.ConfigFilename)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, a.Apply(ctx, canonicalConfig()), context.Canceled)

	bootstrapAfter := readInDir(t, dir, yamlapply.ConfigFilename)
	require.True(t, bytes.Equal(bootstrapBefore, bootstrapAfter),
		"canceled apply must not rewrite the combined config")
}

func TestApply_RejectsInvalidNames(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	cases := []struct {
		label string
		name  string
	}{
		{"path traversal", "../escape"},
		{"slash", "ns/name"},
		{"uppercase", "Grizzly"},
		{"empty", ""},
	}
	bootstrapBefore := readInDir(t, dir, yamlapply.ConfigFilename)

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			c := canonicalConfig()
			c.Backends[0].Name = tc.name
			c.Routes[0].Name = tc.name
			c.Policies[0].Name = tc.name
			require.Error(t, a.Apply(t.Context(), c))
		})
	}

	bootstrapAfter := readInDir(t, dir, yamlapply.ConfigFilename)
	require.True(t, bytes.Equal(bootstrapBefore, bootstrapAfter),
		"rejected applies must not change the combined config")
}

func TestApply_RejectsMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	t.Run("missing backend", func(t *testing.T) {
		c := canonicalConfig()
		c.Backends = nil
		require.Error(t, a.Apply(t.Context(), c))
	})

	t.Run("unresolved host", func(t *testing.T) {
		c := canonicalConfig()
		target := c.Backends[0].Target.(agentgateway.HTTPTarget)
		target.Host = ""
		c.Backends[0].Target = target
		require.Error(t, a.Apply(t.Context(), c))
	})

	t.Run("unresolved port", func(t *testing.T) {
		c := canonicalConfig()
		target := c.Backends[0].Target.(agentgateway.HTTPTarget)
		target.Port = 0
		c.Backends[0].Target = target
		require.Error(t, a.Apply(t.Context(), c))
	})

	t.Run("no transport target", func(t *testing.T) {
		c := canonicalConfig()
		c.Backends[0].Target = nil
		require.Error(t, a.Apply(t.Context(), c))
	})

	t.Run("stdio missing command", func(t *testing.T) {
		c := canonicalConfig()
		c.Backends[0].Target = agentgateway.StdioTarget{}
		require.Error(t, a.Apply(t.Context(), c))
	})

	t.Run("names disagree", func(t *testing.T) {
		c := canonicalConfig()
		c.Routes[0].Name = "other"
		require.Error(t, a.Apply(t.Context(), c))
	})
}

func TestApply_OverwritesStaleContent(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	writeInDir(t, dir, yamlapply.ConfigFilename, []byte("stale: content\n"))

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	raw := readInDir(t, dir, yamlapply.ConfigFilename)
	require.NotContains(t, string(raw), "stale: content")
	require.Contains(t, string(raw), "grizzly.muster.svc.cluster.local")
}

func TestApply_CleansLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	writeInDir(t, dir, yamlapply.ConfigFilename+".tmp", []byte("crashed mid-write"))

	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Name())
	}
	require.Contains(t, names, yamlapply.ConfigFilename)
	require.NotContains(t, names, yamlapply.ConfigFilename+".tmp", "rename must replace any preexisting temp file")
}

func TestDelete_RemovesRouteFromCombinedFile(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	a1 := canonicalConfig()
	a1.Backends[0].Name = fixtureAlpha
	a1.Routes[0].Name = fixtureAlpha
	a1.Routes[0].PathMatch = "/mcp/alpha"
	a1.Policies[0].Name = fixtureAlpha

	a2 := canonicalConfig()
	a2.Backends[0].Name = fixtureBravo
	a2.Routes[0].Name = fixtureBravo
	a2.Routes[0].PathMatch = "/mcp/bravo"
	a2.Policies[0].Name = fixtureBravo

	require.NoError(t, a.Apply(t.Context(), a1))
	require.NoError(t, a.Apply(t.Context(), a2))

	require.NoError(t, a.Delete(t.Context(), fixtureAlpha))

	raw := readInDir(t, dir, yamlapply.ConfigFilename)
	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	require.Len(t, cfg.Binds[0].Listeners[0].Routes, 1)
	require.Equal(t, fixtureBravo, cfg.Binds[0].Listeners[0].Routes[0].Name)
}

func TestDelete_NoopWhenMissing(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	bootstrapBefore := readInDir(t, dir, yamlapply.ConfigFilename)
	require.NoError(t, a.Delete(t.Context(), "absent"))
	bootstrapAfter := readInDir(t, dir, yamlapply.ConfigFilename)
	require.True(t, bytes.Equal(bootstrapBefore, bootstrapAfter),
		"delete of an unknown route must not rewrite the combined config")
}

func TestDelete_RejectsInvalidName(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)
	require.Error(t, a.Delete(t.Context(), "../escape"))
	require.Error(t, a.Delete(t.Context(), ""))
}

func TestDelete_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)
	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, a.Delete(ctx, fixtureGrizzly), context.Canceled)
	require.FileExists(t, filepath.Join(dir, yamlapply.ConfigFilename), "canceled delete must leave the file in place")
}

func TestNewApplier_RequiresDir(t *testing.T) {
	_, err := yamlapply.NewApplier("")
	require.Error(t, err)
}

func TestNewApplier_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "agw")
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)
	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))
	require.FileExists(t, filepath.Join(dir, yamlapply.ConfigFilename))
}

func TestApply_MatchesGolden(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)
	require.NoError(t, a.Apply(t.Context(), canonicalConfig()))

	assertMatchesGolden(t, dir, "grizzly.yaml", yamlapply.ConfigFilename)
}

func TestApply_StdioMatchesGolden(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)
	require.NoError(t, a.Apply(t.Context(), stdioConfig()))

	assertMatchesGolden(t, dir, "grizzly-stdio.yaml", yamlapply.ConfigFilename)
}

func TestApply_ConcurrentDistinctNames(t *testing.T) {
	dir := t.TempDir()
	a, err := yamlapply.NewApplier(dir)
	require.NoError(t, err)

	const goroutines = 8
	done := make(chan error, goroutines)
	for i := range goroutines {
		go func(idx int) {
			c := canonicalConfig()
			n := fmt.Sprintf("srv-%d", idx)
			c.Backends[0].Name = n
			c.Routes[0].Name = n
			c.Policies[0].Name = n
			c.Routes[0].PathMatch = "/mcp/" + n
			done <- a.Apply(t.Context(), c)
		}(i)
	}
	for range goroutines {
		require.NoError(t, <-done)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "concurrent applies must converge on the single combined file")

	raw := readInDir(t, dir, yamlapply.ConfigFilename)
	var cfg yamlapply.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	require.Len(t, cfg.Binds[0].Listeners[0].Routes, goroutines)
}

func assertMatchesGolden(t *testing.T, dir, goldenName, fileName string) {
	t.Helper()
	got := readInDir(t, dir, fileName)

	testdataRoot, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	defer func() { _ = testdataRoot.Close() }()

	want, err := testdataRoot.ReadFile(goldenName)
	if errors.Is(err, os.ErrNotExist) && os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, testdataRoot.WriteFile(goldenName, got, 0o600))
		t.Skip("golden file created")
	}
	require.NoError(t, err, "golden file missing — regenerate with UPDATE_GOLDEN=1 go test ...")

	if !bytes.Equal(got, want) {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			require.NoError(t, testdataRoot.WriteFile(goldenName, got, 0o600))
			t.Skip("golden file refreshed")
		}
		t.Fatalf("golden mismatch:\n--- want\n%s\n--- got\n%s", want, got)
	}
}
