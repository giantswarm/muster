package yaml

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

func TestDelete_DropsLockEntry(t *testing.T) {
	dir := t.TempDir()
	applier, err := NewApplier(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = applier.Close() })

	const n = 100
	for i := range n {
		name := fmt.Sprintf("srv-%d", i)
		config := agentgateway.Config{
			Name:      name,
			Namespace: "muster",
			Backends: []agentgateway.Backend{{
				Name: name,
				Target: agentgateway.HTTPTarget{
					Protocol: agentgateway.StreamableHTTP,
					Host:     "example.invalid",
					Port:     8080,
					Path:     "/mcp",
				},
			}},
			Routes: []agentgateway.Route{{
				Name:       name,
				PathMatch:  "/mcp/" + name,
				BackendRef: name,
				PolicyRef:  name,
			}},
			Policies: []agentgateway.Policy{{
				Name:  name,
				Authn: agentgateway.Authn{Type: agentgateway.AuthnTypeNone},
			}},
		}
		require.NoError(t, applier.Apply(t.Context(), config))
		require.NoError(t, applier.Delete(t.Context(), name))
	}

	applier.mu.Lock()
	defer applier.mu.Unlock()
	require.Empty(t, applier.locks, "Delete must not leak per-name mutexes")
}

func TestDelete_ConcurrentDropsLockEntry(t *testing.T) {
	dir := t.TempDir()
	applier, err := NewApplier(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = applier.Close() })

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("srv-%d", idx)
			config := agentgateway.Config{
				Name:      name,
				Namespace: "muster",
				Backends: []agentgateway.Backend{{
					Name: name,
					Target: agentgateway.HTTPTarget{
						Protocol: agentgateway.StreamableHTTP,
						Host:     "example.invalid",
						Port:     8080,
						Path:     "/mcp",
					},
				}},
				Routes: []agentgateway.Route{{
					Name:       name,
					PathMatch:  "/mcp/" + name,
					BackendRef: name,
					PolicyRef:  name,
				}},
				Policies: []agentgateway.Policy{{
					Name:  name,
					Authn: agentgateway.Authn{Type: agentgateway.AuthnTypeNone},
				}},
			}
			require.NoError(t, applier.Apply(t.Context(), config))
			require.NoError(t, applier.Delete(t.Context(), name))
		}(i)
	}
	wg.Wait()

	applier.mu.Lock()
	defer applier.mu.Unlock()
	require.Empty(t, applier.locks, "concurrent Delete must not leak per-name mutexes")
}
