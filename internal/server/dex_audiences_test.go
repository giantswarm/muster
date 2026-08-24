package server

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/mcp-oauth/security"

	"github.com/giantswarm/muster/internal/config"
)

// errCollect stands in for an apiserver read failure.
var errCollect = errors.New("list MCPServers: connection refused")

// noAudiences is an AudienceResolver that reports nothing.
func noAudiences() []string { return nil }

func staticCollector(audiences ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) {
		return slices.Clone(audiences), nil
	}
}

// audienceSource is a collector whose result the test changes, standing in for
// MCPServer resources that reconcile after muster starts.
type audienceSource struct {
	mu        sync.Mutex
	audiences []string
	err       error
	calls     atomic.Int64
}

func (s *audienceSource) collect(context.Context) ([]string, error) {
	s.calls.Add(1)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}
	return slices.Clone(s.audiences), nil
}

func (s *audienceSource) set(audiences ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audiences = audiences
}

func (s *audienceSource) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// TestNewDexProviderConfigCarriesTheResolver covers an MCPServer that registers
// after muster starts. The audience must reach the Dex provider without a
// restart, and mcp-oauth owns the scope formatting and its validation.
func TestNewDexProviderConfigCarriesTheResolver(t *testing.T) {
	cfg := config.OAuthServerConfig{
		Dex: config.DexConfig{
			IssuerURL:    "https://dex.example.com",
			ClientID:     "muster",
			ClientSecret: "secret",
		},
	}

	resolver := newDexAudienceResolver(staticCollector("dex-k8s-authenticator"), slog.New(slog.DiscardHandler))
	resolver.prime()

	dexConfig := newDexProviderConfig(cfg, "https://muster.example.com/oauth/callback", nil, resolver.Resolve)

	require.NotNil(t, dexConfig.AudienceResolver)
	assert.Equal(t, []string{"dex-k8s-authenticator"}, dexConfig.AudienceResolver())
	assert.Equal(t, dexOAuthScopes, dexConfig.Scopes)
	assert.NotSame(t, &dexOAuthScopes[0], &dexConfig.Scopes[0],
		"the provider must not alias muster's base scope slice")
}

// TestDexAudienceResolverDoesNoWorkOnTheLoginPath is the contract
// dex.Config.AudienceResolver states: the provider calls it from request
// goroutines, up to twice per authorization request, and it must read cached
// state rather than block on a network call. A collector call here would put an
// uncached apiserver list on the login path, where a slow or failing apiserver
// would serialise every login behind it.
func TestDexAudienceResolverDoesNoWorkOnTheLoginPath(t *testing.T) {
	source := &audienceSource{audiences: []string{"dex-k8s-authenticator"}}
	resolver := newDexAudienceResolver(source.collect, slog.New(slog.DiscardHandler))

	resolver.prime()
	require.Equal(t, int64(1), source.calls.Load())

	for range 100 {
		require.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
	}
	assert.Equal(t, int64(1), source.calls.Load(), "Resolve must not call the collector")

	// The same holds while the read is failing, which is when an unbounded
	// login path would hurt most.
	source.fail(errCollect)

	for range 100 {
		require.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
	}
	assert.Equal(t, int64(1), source.calls.Load(), "a failing read must not reach the login path")
}

// TestDexAudienceResolverRefreshesInTheBackground covers the gazelle sequence:
// muster starts before its MCPServer resources exist, and the audience reaches
// a login one refresh interval later without a restart.
func TestDexAudienceResolverRefreshesInTheBackground(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &audienceSource{}
		resolver := newDexAudienceResolver(source.collect, slog.New(slog.DiscardHandler))

		resolver.prime()
		resolver.start(nil)
		defer resolver.stop()

		require.Empty(t, resolver.Resolve())

		source.set("dex-k8s-authenticator")

		time.Sleep(dexAudienceRefreshInterval)
		synctest.Wait()

		assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
	})
}

// TestDexAudienceResolverKeepsTheLastKnownSetOnFailure is the reason the
// resolver holds state at all. A token minted without an audience cannot be
// repaired without a new login, so a failed read must not be served as "no
// MCPServer needs an audience".
func TestDexAudienceResolverKeepsTheLastKnownSetOnFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &audienceSource{audiences: []string{"dex-k8s-authenticator"}}
		resolver := newDexAudienceResolver(source.collect, slog.New(slog.DiscardHandler))

		resolver.prime()
		resolver.start(nil)
		defer resolver.stop()

		require.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())

		source.fail(errCollect)

		time.Sleep(dexAudienceRefreshInterval)
		synctest.Wait()

		assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve(),
			"a failed read must serve the last known set, not an empty one")
	})
}

// TestDexAudienceResolverReportsNothingWhenNothingIsKnown covers the cold start:
// the first read fails before any set is known. The login proceeds with muster's
// own client only, because AudienceResolver cannot fail the request. The failure
// is logged as an error, and no line reports an empty set, which would read as
// "no MCPServer needs an audience".
func TestDexAudienceResolverReportsNothingWhenNothingIsKnown(t *testing.T) {
	handler := &recordingHandler{}
	resolver := newDexAudienceResolver(func(context.Context) ([]string, error) {
		return nil, errCollect
	}, slog.New(handler))

	resolver.prime()
	resolver.report()

	assert.Empty(t, resolver.Resolve())
	assert.Equal(t, []slog.Level{slog.LevelError}, handler.levels())
}

// TestDexAudienceResolverReportsAKnownEmptySet keeps the other cold start
// legible: the read succeeded and no MCPServer needs an audience. That is the
// state the gazelle instance was in, and it is indistinguishable from a failed
// read without this line.
func TestDexAudienceResolverReportsAKnownEmptySet(t *testing.T) {
	handler := &recordingHandler{}
	resolver := newDexAudienceResolver(staticCollector(), slog.New(handler))

	resolver.prime()
	resolver.report()

	assert.Equal(t, []slog.Level{slog.LevelInfo}, handler.levels())
}

// TestDexAudienceResolverBoundsTheCollectorCall pins the deadline. The collector
// lists MCPServers through an uncached Kubernetes client, so an unbounded call
// would hold the refresher forever.
func TestDexAudienceResolverBoundsTheCollectorCall(t *testing.T) {
	var deadline time.Time
	resolver := newDexAudienceResolver(func(ctx context.Context) ([]string, error) {
		deadline, _ = ctx.Deadline()
		return nil, nil
	}, slog.New(slog.DiscardHandler))

	resolver.prime()

	assert.False(t, deadline.IsZero(), "the collector call must carry a deadline")
}

// TestDexAudienceResolverReturnsACopy keeps a caller from writing into the
// resolver's set. The Dex provider appends to the slice it receives.
func TestDexAudienceResolverReturnsACopy(t *testing.T) {
	resolver := newDexAudienceResolver(staticCollector("dex-k8s-authenticator"), slog.New(slog.DiscardHandler))
	resolver.prime()

	first := resolver.Resolve()
	first[0] = "overwritten"

	assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
}

// TestDexAudienceResolverAuditsEachChange pins the record that replaces the
// dropped requestableAudiences allowlist. The set now changes without a muster
// restart, so an MCPServer write that widens the audience of every minted ID
// token must leave a trail. An unchanged set records nothing.
func TestDexAudienceResolverAuditsEachChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &audienceSource{}
		handler := &recordingHandler{}
		resolver := newDexAudienceResolver(source.collect, slog.New(slog.DiscardHandler))

		resolver.prime()
		resolver.start(security.NewAuditor(slog.New(handler), true))
		defer resolver.stop()

		require.Equal(t, 1, handler.count(), "the initial set is one record")

		time.Sleep(dexAudienceRefreshInterval)
		synctest.Wait()
		assert.Equal(t, 1, handler.count(), "an unchanged set must record nothing")

		source.set("dex-k8s-authenticator")
		time.Sleep(dexAudienceRefreshInterval)
		synctest.Wait()

		require.Equal(t, 2, handler.count())
		assert.Equal(t, EventDexAudiencesChanged, handler.lastEventType())
	})
}

// TestDexAudienceResolverStopEndsTheRefresher keeps a muster shutdown from
// leaving a goroutine listing MCPServers.
func TestDexAudienceResolverStopEndsTheRefresher(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &audienceSource{}
		resolver := newDexAudienceResolver(source.collect, slog.New(slog.DiscardHandler))

		resolver.prime()
		resolver.start(nil)
		resolver.stop()

		calls := source.calls.Load()

		time.Sleep(4 * dexAudienceRefreshInterval)
		synctest.Wait()

		assert.Equal(t, calls, source.calls.Load(), "no refresh may run after stop")

		resolver.stop()
	})
}

// TestDexAudienceResolverStopWithoutStart covers the provider that never starts
// a refresher.
func TestDexAudienceResolverStopWithoutStart(t *testing.T) {
	resolver := newDexAudienceResolver(staticCollector(), slog.New(slog.DiscardHandler))

	resolver.stop()
}

// TestDexAudienceResolverIsConcurrencySafe covers parallel logins against the
// refresher. The provider calls Resolve from every authorization request
// handler while the refresher writes the same set.
func TestDexAudienceResolverIsConcurrencySafe(t *testing.T) {
	source := &audienceSource{audiences: []string{"dex-k8s-authenticator"}}
	resolver := newDexAudienceResolver(source.collect, slog.New(slog.DiscardHandler))
	resolver.prime()

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			assert.Equal(t, []string{"dex-k8s-authenticator"}, resolver.Resolve())
		}()
		go func() {
			defer wg.Done()
			resolver.refresh()
			resolver.report()
		}()
	}
	wg.Wait()
}

// recordingHandler records what a logger emits.
type recordingHandler struct {
	mu       sync.Mutex
	recorded []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recorded = append(h.recorded, record)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func (h *recordingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.recorded)
}

func (h *recordingHandler) levels() []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()

	levels := make([]slog.Level, len(h.recorded))
	for i, record := range h.recorded {
		levels[i] = record.Level
	}
	return levels
}

// lastEventType returns the audit event type of the most recent record, or ""
// when it is not an audit record.
func (h *recordingHandler) lastEventType() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.recorded) == 0 {
		return ""
	}

	var eventType string
	h.recorded[len(h.recorded)-1].Attrs(func(attr slog.Attr) bool {
		if attr.Key != "audit" {
			return true
		}
		for _, grouped := range attr.Value.Group() {
			if grouped.Key == "event_type" {
				eventType = grouped.Value.String()
				return false
			}
		}
		return false
	})
	return eventType
}
