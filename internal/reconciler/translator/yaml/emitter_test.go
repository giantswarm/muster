package yaml_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	goyaml "gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/internal/agentgateway/configtypes"
	"github.com/giantswarm/muster/internal/reconciler/translator"
	yamlemit "github.com/giantswarm/muster/internal/reconciler/translator/yaml"
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

func canonicalModel() translator.Model {
	return translator.Model{
		Backends: []translator.Backend{{
			Name:     "grizzly",
			Host:     "grizzly.muster.svc.cluster.local",
			Port:     8443,
			Path:     "/mcp",
			Protocol: translator.ProtocolStreamableHTTP,
		}},
		Routes: []translator.Route{{
			Name:       "grizzly",
			PathMatch:  "/mcp/grizzly",
			BackendRef: "grizzly",
			PolicyRef:  "grizzly",
		}},
		Policies: []translator.Policy{{
			Name:  "grizzly",
			Authn: translator.AuthnConfig{Type: translator.AuthnTypeOAuth, ForwardToken: true},
		}},
	}
}

func sseModel() translator.Model {
	m := canonicalModel()
	m.Backends[0].Protocol = translator.ProtocolSSE
	m.Backends[0].Path = "/sse"
	m.Policies[0].Authn = translator.AuthnConfig{Type: translator.AuthnTypeNone}
	return m
}

func TestEmit_WritesFilePerMCPServer(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))

	data := readInDir(t, dir, "grizzly.yaml")

	first := strings.SplitN(string(data), "\n", 2)[0]
	require.Equal(t, "# yaml-language-server: $schema="+yamlemit.SchemaURL, first)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the target file should remain after a successful emit")
	require.Equal(t, "grizzly.yaml", entries[0].Name())
}

func TestEmit_ReEmitIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))
	first := readInDir(t, dir, "grizzly.yaml")

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))
	second := readInDir(t, dir, "grizzly.yaml")

	require.True(t, bytes.Equal(first, second), "re-emit must produce byte-identical output")
}

func TestEmit_RoundTripUnmarshal(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))
	raw := readInDir(t, dir, "grizzly.yaml")

	var cfg configtypes.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))

	require.Len(t, cfg.Binds, 1)
	require.Equal(t, yamlemit.DefaultListenerPort, cfg.Binds[0].Port)
	require.Len(t, cfg.Binds[0].Listeners, 1)

	listener := cfg.Binds[0].Listeners[0]
	require.Equal(t, "grizzly", listener.Name)
	require.Len(t, listener.Routes, 1)

	route := listener.Routes[0]
	require.Equal(t, "grizzly", route.Name)
	require.Len(t, route.Matches, 1)
	require.NotNil(t, route.Matches[0].Path)
	require.Equal(t, "/mcp/grizzly", route.Matches[0].Path.PathPrefix)

	require.Len(t, route.Backends, 1)
	require.NotNil(t, route.Backends[0].MCP)
	require.Len(t, route.Backends[0].MCP.Targets, 1)

	target := route.Backends[0].MCP.Targets[0]
	require.Equal(t, "grizzly", target.Name)
	require.NotNil(t, target.MCP)
	require.Nil(t, target.SSE)
	require.Equal(t, "grizzly.muster.svc.cluster.local", target.MCP.Host)
	require.Equal(t, uint16(8443), target.MCP.Port)
	require.Equal(t, "/mcp", target.MCP.Path)

	require.NotNil(t, route.Policies)
	require.NotNil(t, route.Policies.BackendAuth)
	require.NotNil(t, route.Policies.BackendAuth.Passthrough)
}

func TestEmit_SSEProtocol(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	require.NoError(t, e.Emit(t.Context(), sseModel()))
	raw := readInDir(t, dir, "grizzly.yaml")

	var cfg configtypes.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	target := cfg.Binds[0].Listeners[0].Routes[0].Backends[0].MCP.Targets[0]
	require.Nil(t, target.MCP)
	require.NotNil(t, target.SSE)
	require.Equal(t, "/sse", target.SSE.Path)

	require.Nil(t, cfg.Binds[0].Listeners[0].Routes[0].Policies,
		"AuthnTypeNone with no ForwardToken must omit the policies block")
}

func TestEmit_CustomListenerPort(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir, yamlemit.WithListenerPort(9090))
	require.NoError(t, err)

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))

	raw := readInDir(t, dir, "grizzly.yaml")

	var cfg configtypes.LocalConfig
	require.NoError(t, goyaml.Unmarshal(raw, &cfg))
	require.Equal(t, uint16(9090), cfg.Binds[0].Port)
}

func TestEmit_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, e.Emit(ctx, canonicalModel()), context.Canceled)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "canceled emit must not touch the filesystem")
}

func TestEmit_RejectsInvalidNames(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
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
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m := canonicalModel()
			m.Backends[0].Name = tc.name
			m.Routes[0].Name = tc.name
			m.Policies[0].Name = tc.name
			require.Error(t, e.Emit(t.Context(), m))
		})
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestEmit_RejectsMalformedModel(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	t.Run("missing backend", func(t *testing.T) {
		m := canonicalModel()
		m.Backends = nil
		require.Error(t, e.Emit(t.Context(), m))
	})

	t.Run("unresolved host", func(t *testing.T) {
		m := canonicalModel()
		m.Backends[0].Host = ""
		require.Error(t, e.Emit(t.Context(), m))
	})

	t.Run("unresolved port", func(t *testing.T) {
		m := canonicalModel()
		m.Backends[0].Port = 0
		require.Error(t, e.Emit(t.Context(), m))
	})

	t.Run("unsupported protocol", func(t *testing.T) {
		m := canonicalModel()
		m.Backends[0].Protocol = translator.Protocol("HBONE")
		require.Error(t, e.Emit(t.Context(), m))
	})

	t.Run("names disagree", func(t *testing.T) {
		m := canonicalModel()
		m.Routes[0].Name = "other"
		require.Error(t, e.Emit(t.Context(), m))
	})
}

func TestEmit_OverwritesStaleContent(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	writeInDir(t, dir, "grizzly.yaml", []byte("stale: content\n"))

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))

	raw := readInDir(t, dir, "grizzly.yaml")
	require.NotContains(t, string(raw), "stale: content")
	require.Contains(t, string(raw), "grizzly.muster.svc.cluster.local")
}

func TestEmit_CleansLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	writeInDir(t, dir, "grizzly.yaml.tmp", []byte("crashed mid-write"))

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Name())
	}
	require.Contains(t, names, "grizzly.yaml")
	require.NotContains(t, names, "grizzly.yaml.tmp", "rename must replace any preexisting temp file")
}

func TestDelete_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	require.NoError(t, e.Emit(t.Context(), canonicalModel()))
	require.FileExists(t, filepath.Join(dir, "grizzly.yaml"))

	require.NoError(t, e.Delete(t.Context(), "grizzly"))
	_, err = os.Stat(filepath.Join(dir, "grizzly.yaml"))
	require.True(t, os.IsNotExist(err))
}

func TestDelete_NoopWhenMissing(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)
	require.NoError(t, e.Delete(t.Context(), "absent"))
}

func TestDelete_RejectsInvalidName(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)
	require.Error(t, e.Delete(t.Context(), "../escape"))
	require.Error(t, e.Delete(t.Context(), ""))
}

func TestDelete_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)
	require.NoError(t, e.Emit(t.Context(), canonicalModel()))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, e.Delete(ctx, "grizzly"), context.Canceled)
	require.FileExists(t, filepath.Join(dir, "grizzly.yaml"), "canceled delete must leave the file in place")
}

func TestNew_RequiresDir(t *testing.T) {
	_, err := yamlemit.New("")
	require.Error(t, err)
}

func TestNew_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "agw")
	e, err := yamlemit.New(dir)
	require.NoError(t, err)
	require.NoError(t, e.Emit(t.Context(), canonicalModel()))
	require.FileExists(t, filepath.Join(dir, "grizzly.yaml"))
}

func TestEmit_MatchesGolden(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)
	require.NoError(t, e.Emit(t.Context(), canonicalModel()))

	got := readInDir(t, dir, "grizzly.yaml")

	testdataRoot, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	defer func() { _ = testdataRoot.Close() }()

	want, err := testdataRoot.ReadFile("grizzly.yaml")
	if errors.Is(err, os.ErrNotExist) && os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, testdataRoot.WriteFile("grizzly.yaml", got, 0o600))
		t.Skip("golden file created")
	}
	require.NoError(t, err, "golden file missing — regenerate with UPDATE_GOLDEN=1 go test ...")

	if !bytes.Equal(got, want) {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			require.NoError(t, testdataRoot.WriteFile("grizzly.yaml", got, 0o600))
			t.Skip("golden file refreshed")
		}
		t.Fatalf("golden mismatch:\n--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestEmit_ConcurrentDistinctNames(t *testing.T) {
	dir := t.TempDir()
	e, err := yamlemit.New(dir)
	require.NoError(t, err)

	const goroutines = 8
	done := make(chan error, goroutines)
	for i := range goroutines {
		go func(idx int) {
			m := canonicalModel()
			n := "srv-" + string(rune('a'+idx))
			m.Backends[0].Name = n
			m.Routes[0].Name = n
			m.Policies[0].Name = n
			m.Routes[0].PathMatch = "/mcp/" + n
			done <- e.Emit(t.Context(), m)
		}(i)
	}
	for range goroutines {
		require.NoError(t, <-done)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, goroutines)
}
