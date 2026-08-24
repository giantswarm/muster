package server

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

// errCollect stands in for an apiserver read failure on the login path.
var errCollect = errors.New("list MCPServers: connection refused")

func staticCollector(audiences ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) {
		return slices.Clone(audiences), nil
	}
}

// TestNewDexProviderConfigResolvesAudiencesPerCall covers an MCPServer that
// registers after muster starts. The Dex provider calls AudienceResolver on
// every authorization request, so the audience must reach the resolver without
// a restart. mcp-oauth owns the scope formatting and its validation.
func TestNewDexProviderConfigResolvesAudiencesPerCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.OAuthServerConfig{
			Dex: config.DexConfig{
				IssuerURL:    "https://dex.example.com",
				ClientID:     "muster",
				ClientSecret: "secret",
			},
		}

		var (
			mu        sync.Mutex
			audiences []string
		)
		collect := func(context.Context) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(audiences), nil
		}

		dexConfig := newDexProviderConfig(cfg, "https://muster.example.com/oauth/callback", nil,
			slog.New(slog.DiscardHandler), collect)

		require.NotNil(t, dexConfig.AudienceResolver)
		assert.Equal(t, dexOAuthScopes, dexConfig.Scopes)
		assert.NotSame(t, &dexOAuthScopes[0], &dexConfig.Scopes[0],
			"the provider must not alias muster's base scope slice")

		// No MCPServer is registered yet, which is the state during a namespace
		// cutover: muster starts before its MCPServer resources exist.
		require.Empty(t, dexConfig.AudienceResolver())

		mu.Lock()
		audiences = []string{"dex-k8s-authenticator"}
		mu.Unlock()

		// The resolver reuses one set for the TTL, so the new audience reaches a
		// login one TTL later. The bubble makes that wait instant.
		time.Sleep(dexAudienceTTL)

		assert.Equal(t, []string{"dex-k8s-authenticator"}, dexConfig.AudienceResolver())
	})
}

// TestDexAudienceResolverBoundsCollectorCalls pins the cost of a per-request
// resolver. The collector lists MCPServers through an uncached Kubernetes
// client, so every login would otherwise reach the apiserver.
func TestDexAudienceResolverBoundsCollectorCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int
		resolver := newDexAudienceResolver(func(context.Context) ([]string, error) {
			calls++
			return []string{"dex-k8s-authenticator"}, nil
		}, slog.New(slog.DiscardHandler))

		for range 5 {
			require.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		}
		assert.Equal(t, 1, calls, "logins inside the TTL must share one collector call")

		time.Sleep(dexAudienceTTL)
		require.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		assert.Equal(t, 2, calls, "the first login after the TTL must refresh the set")
	})
}

// TestDexAudienceResolverKeepsTheLastKnownSetOnFailure is the reason the
// resolver holds state at all. A token minted without an audience cannot be
// repaired without a new login, so a failed read must not be served as "no
// MCPServer needs an audience".
func TestDexAudienceResolverKeepsTheLastKnownSetOnFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			calls int
			fail  bool
		)
		resolver := newDexAudienceResolver(func(context.Context) ([]string, error) {
			calls++
			if fail {
				return nil, errCollect
			}
			return []string{"dex-k8s-authenticator"}, nil
		}, slog.New(slog.DiscardHandler))

		require.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		require.Equal(t, 1, calls)

		fail = true
		time.Sleep(dexAudienceTTL)

		assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve(),
			"a failed read must serve the last known set, not an empty one")
		require.Equal(t, 2, calls)

		// A failed read does not stamp the fetch time, so the next login retries
		// at once instead of waiting out the TTL.
		assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		assert.Equal(t, 3, calls, "a failed read must not start a new TTL window")
	})
}

// TestDexAudienceResolverReportsNoAudiencesWhenNothingIsKnown covers the cold
// start: the first read fails before any set is known. The login proceeds with
// muster's own client only, because AudienceResolver cannot fail the request.
func TestDexAudienceResolverReportsNoAudiencesWhenNothingIsKnown(t *testing.T) {
	handler := &countingHandler{}
	resolver := newDexAudienceResolver(func(context.Context) ([]string, error) {
		return nil, errCollect
	}, slog.New(handler))

	assert.Empty(t, resolver.Resolve())
	assert.Equal(t, []slog.Level{slog.LevelError, slog.LevelInfo}, handler.levels(),
		"an unknown set must be logged as an error, not as an empty set")
}

// TestDexAudienceResolverDistinguishesAKnownEmptySet keeps a successful read of
// zero audiences from being retried as if nothing were known.
func TestDexAudienceResolverDistinguishesAKnownEmptySet(t *testing.T) {
	var calls int
	resolver := newDexAudienceResolver(func(context.Context) ([]string, error) {
		calls++
		return nil, nil
	}, slog.New(slog.DiscardHandler))

	require.Empty(t, resolver.Resolve())
	require.Empty(t, resolver.Resolve())
	assert.Equal(t, 1, calls, "a known empty set must be reused for the TTL")
}

// TestDexAudienceResolverBoundsTheCollectorCall pins the deadline. The resolver
// holds its mutex across the call, so an unbounded call blocks every
// authorization request.
func TestDexAudienceResolverBoundsTheCollectorCall(t *testing.T) {
	var deadline time.Time
	resolver := newDexAudienceResolver(func(ctx context.Context) ([]string, error) {
		deadline, _ = ctx.Deadline()
		return nil, nil
	}, slog.New(slog.DiscardHandler))

	resolver.Resolve()

	assert.False(t, deadline.IsZero(), "the collector call must carry a deadline")
}

// TestDexAudienceResolverReturnsACopy keeps a caller from writing into the
// resolver's set. The Dex provider appends to the slice it receives.
func TestDexAudienceResolverReturnsACopy(t *testing.T) {
	resolver := newDexAudienceResolver(staticCollector("dex-k8s-authenticator"), slog.New(slog.DiscardHandler))

	first := resolver.Resolve()
	first[0] = "overwritten"

	assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
}

// TestDexAudienceResolverLogsEachChange pins the forensics the per-request
// resolver would otherwise cost: the audience set a login requests is logged
// when it changes, and a login that requests an unchanged set logs nothing.
func TestDexAudienceResolverLogsEachChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			mu        sync.Mutex
			audiences []string
		)
		setAudiences := func(values ...string) {
			mu.Lock()
			defer mu.Unlock()
			audiences = values
		}
		handler := &countingHandler{}

		resolver := newDexAudienceResolver(func(context.Context) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(audiences), nil
		}, slog.New(handler))

		// An empty set is a state worth one line: it is what the gazelle instance
		// reported, and it is indistinguishable from "no Dex audiences configured"
		// without it.
		require.Empty(t, resolver.Resolve())
		require.Equal(t, 1, handler.count())

		resolver.Resolve()
		assert.Equal(t, 1, handler.count(), "an unchanged set must not log per login")

		setAudiences("dex-k8s-authenticator")
		time.Sleep(dexAudienceTTL)
		assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		assert.Equal(t, 2, handler.count())

		resolver.Resolve()
		assert.Equal(t, 2, handler.count())

		setAudiences("dex-k8s-authenticator", "another-client")
		time.Sleep(dexAudienceTTL)
		assert.Equal(t, []string{"dex-k8s-authenticator", "another-client"}, resolver.Resolve())
		assert.Equal(t, 3, handler.count())
	})
}

// TestDexAudienceResolverIsConcurrencySafe covers parallel logins. The provider
// calls the resolver from every authorization request handler.
func TestDexAudienceResolverIsConcurrencySafe(t *testing.T) {
	resolver := newDexAudienceResolver(staticCollector("dex-k8s-authenticator"), slog.New(slog.DiscardHandler))

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		}()
	}
	wg.Wait()
}

// countingHandler records the levels a logger emits. The audience resolver runs
// on every authorization request, so the count is the assertion that matters.
type countingHandler struct {
	mu       sync.Mutex
	recorded []slog.Level
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recorded = append(h.recorded, record.Level)
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *countingHandler) WithGroup(string) slog.Handler { return h }

func (h *countingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.recorded)
}

func (h *countingHandler) levels() []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.recorded)
}
