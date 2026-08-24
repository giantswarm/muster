package server

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"
)

const (
	// dexAudienceTTL bounds how long one resolved audience set is reused.
	//
	// The provider calls the resolver on every authorization request, and the
	// collector lists MCPServers through an uncached Kubernetes client, so an
	// unmemoized resolver puts a list call on each login and lets an
	// unauthenticated request loop drive that traffic.
	dexAudienceTTL = 10 * time.Second

	// dexAudienceTimeout bounds one collector call. The resolver holds its
	// mutex across the call, so an unbounded call blocks every authorization
	// request.
	dexAudienceTimeout = 5 * time.Second
)

// dexAudienceResolver reports the Dex cross-client audiences a login requests.
//
// It answers from the last set it read successfully. A read that fails leaves
// that set in place: an audience muster cannot confirm is not the same as an
// audience no MCPServer needs, and a token minted without one cannot be
// repaired without a new login.
type dexAudienceResolver struct {
	collect func(context.Context) ([]string, error)
	ttl     time.Duration
	timeout time.Duration
	logger  *slog.Logger

	mu sync.Mutex
	// haveKnown reports whether known holds the result of a successful read.
	// A successful read of zero audiences is a known empty set, not an
	// unknown one.
	haveKnown bool
	known     []string
	fetchedAt time.Time
	reported  []string
	logged    bool
}

// newDexAudienceResolver returns a resolver over collect.
func newDexAudienceResolver(collect func(context.Context) ([]string, error), logger *slog.Logger) *dexAudienceResolver {
	return &dexAudienceResolver{
		collect: collect,
		ttl:     dexAudienceTTL,
		timeout: dexAudienceTimeout,
		logger:  logger,
	}
}

// Resolve satisfies dex.Config.AudienceResolver. The caller receives a copy.
func (r *dexAudienceResolver) Resolve() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.haveKnown || time.Since(r.fetchedAt) >= r.ttl {
		r.refresh()
	}

	r.report()

	return slices.Clone(r.known)
}

// refresh reads a new set. A failed read does not stamp fetchedAt, so the next
// authorization request retries at once instead of after the TTL.
//
// The caller holds r.mu.
func (r *dexAudienceResolver) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	audiences, err := r.collect(ctx)
	switch {
	case err == nil:
		r.haveKnown = true
		r.known = audiences
		r.fetchedAt = time.Now()
	case !r.haveKnown:
		r.logger.Error("Cannot read the cross-client audiences MCPServers require, and none are known yet",
			"error", err,
			"effect", "this login mints an ID token carrying muster's own client only, and a forwardToken backend rejects it")
	default:
		r.logger.Warn("Cannot read the cross-client audiences MCPServers require, serving the last known set",
			"error", err,
			"audiences", r.known)
	}
}

// report logs the audience set on the first call and whenever it changes. The
// resolver runs on every authorization request, so an unconditional line would
// repeat the set once per login.
//
// Without this line no log records which audiences a login requested: mcp-oauth
// logs only the audiences it rejects.
//
// The caller holds r.mu.
func (r *dexAudienceResolver) report() {
	if r.logged && slices.Equal(r.reported, r.known) {
		return
	}
	r.reported = slices.Clone(r.known)
	r.logged = true

	r.logger.Info("Cross-client audiences requested from Dex",
		"audiences", r.known,
		"count", len(r.known))
}
