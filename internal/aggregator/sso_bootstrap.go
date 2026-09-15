package aggregator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// ssoConnectOutcome is what one server's connect in a session's fan-out ended
// in.
type ssoConnectOutcome int

const (
	// ssoConnectFailed: the connect was attempted and did not produce a
	// connection (the failure is recorded on the ssoTracker).
	ssoConnectFailed ssoConnectOutcome = iota
	// ssoConnected: a connection was made and pooled.
	ssoConnected
	// ssoAlreadyConnected: nothing to do -- the session was authenticated to
	// the server with a live pooled client, or the person holds no grant for
	// a subject-scoped server.
	ssoAlreadyConnected
)

// ssoBootstrapGrace is how long the fan-out waits for its connects past
// initSSOTimeout before it reports itself finished regardless: a connect that
// ignores its cancelled context must not hold a session's requests forever.
const ssoBootstrapGrace = 5 * time.Second

// ssoBootstrap is one session's fan-out to the servers it authenticates to per
// session -- the SSO connects (token exchange, token forwarding) and the
// person's subject-scoped grants. It is started by the session's first request
// and finishes in the background: the request that started it is answered
// meanwhile (#1226). A request that needs one server waits for that server's
// connect alone (waitServer); one that needs the whole catalogue waits for the
// fan-out (wait). Both waits end with the caller's context.
type ssoBootstrap struct {
	sessionID string
	started   time.Time
	done      chan struct{}

	mu      sync.Mutex
	servers map[string]chan struct{} // closed when the server's connect finished, whatever the outcome
}

func newSSOBootstrap(sessionID string, servers []string) *ssoBootstrap {
	b := &ssoBootstrap{
		sessionID: sessionID,
		started:   time.Now(),
		done:      make(chan struct{}),
		servers:   make(map[string]chan struct{}, len(servers)),
	}
	for _, name := range servers {
		b.servers[name] = make(chan struct{})
	}
	return b
}

// serverFinished marks the server's connect as finished. Safe to call more
// than once and for a server the fan-out does not cover.
func (b *ssoBootstrap) serverFinished(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.servers[name]
	if !ok {
		return
	}
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// finish marks the whole fan-out as finished, its servers included.
func (b *ssoBootstrap) finish() {
	b.mu.Lock()
	for _, ch := range b.servers {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	b.mu.Unlock()
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// covers reports whether the fan-out connects the server.
func (b *ssoBootstrap) covers(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.servers[name]
	return ok
}

// waitServer blocks until the server's connect finished or ctx is done, and
// reports how long it waited. A server the fan-out does not cover needs no
// wait.
func (b *ssoBootstrap) waitServer(ctx context.Context, name string) time.Duration {
	b.mu.Lock()
	ch, ok := b.servers[name]
	b.mu.Unlock()
	if !ok {
		return 0
	}
	return waitOn(ctx, ch)
}

// wait blocks until the fan-out finished or ctx is done, and reports how long
// it waited.
func (b *ssoBootstrap) wait(ctx context.Context) time.Duration {
	return waitOn(ctx, b.done)
}

func waitOn(ctx context.Context, ch <-chan struct{}) time.Duration {
	select {
	case <-ch:
		return 0
	default:
	}
	start := time.Now()
	select {
	case <-ch:
	case <-ctx.Done():
	}
	return time.Since(start)
}

// sessionBootstrapPlan is what a session's fan-out has to connect: the
// session-authenticated SSO servers not yet connected and the subject-scoped
// servers the person may hold a grant for.
type sessionBootstrapPlan struct {
	sso    []*ServerInfo
	grants []*ServerInfo
}

func (p sessionBootstrapPlan) serverNames() []string {
	names := make([]string, 0, len(p.sso)+len(p.grants))
	for _, info := range p.sso {
		names = append(names, info.Name)
	}
	for _, info := range p.grants {
		names = append(names, info.Name)
	}
	return names
}

func (p sessionBootstrapPlan) empty() bool {
	return len(p.sso) == 0 && len(p.grants) == 0
}

// planSessionBootstrap selects the servers the session's fan-out connects.
// SSO servers the ssoTracker holds in failure backoff for the person are left
// out, so a persistently failing exchange retries on the backoff schedule and
// not on every new session. Subject-scoped servers are included whenever the
// session is not authenticated to them: adopting a grant the person does not
// hold is one store read and ends at once.
func (a *AggregatorServer) planSessionBootstrap(sso ssoSession) sessionBootstrapPlan {
	var plan sessionBootstrapPlan
	servers := a.registry.GetAllServers()
	var skippedNotAuthRequired, skippedNotSSO, skippedPriorFailure int
	for _, info := range servers {
		if !info.RequiresSessionAuth() {
			skippedNotAuthRequired++
			continue
		}
		if !ShouldUseTokenExchange(info) && !ShouldUseTokenForwarding(info) {
			skippedNotSSO++
			if a.sessionMayAdoptGrant(info, sso) {
				plan.grants = append(plan.grants, info)
			}
			continue
		}
		if a.ssoTracker != nil && a.ssoTracker.HasSSOFailed(sso.userID, info.Name) {
			fc := a.ssoTracker.GetFailureCount(sso.userID, info.Name)
			logging.Debug("Aggregator", "SSO: skipping %s for user %s (failureCount=%d, backoff=%v)",
				info.Name, logging.TruncateIdentifier(sso.userID), fc, ssoBackoffDuration(fc))
			skippedPriorFailure++
			continue
		}
		plan.sso = append(plan.sso, info)
	}

	logging.Info("Aggregator", "SSO: initSSOForSession filter results: total=%d, pending=%d, grants=%d, skippedNotAuthRequired=%d, skippedNotSSO=%d, skippedPriorFailure=%d",
		len(servers), len(plan.sso), len(plan.grants), skippedNotAuthRequired, skippedNotSSO, skippedPriorFailure)
	return plan
}

// sessionMayAdoptGrant reports whether the fan-out should try to connect the
// session to a subject-scoped server with the person's grant: the server files
// grants under the person, the caller has a subject, and the session is not
// authenticated to the server yet.
func (a *AggregatorServer) sessionMayAdoptGrant(info *ServerInfo, sso ssoSession) bool {
	if sso.userID == "" || a.authStore == nil || !holdsSubjectGrants(info) {
		return false
	}
	authenticated, _ := a.authStore.IsAuthenticated(context.Background(), sso.sessionID, info.Name)
	return !authenticated
}

// beginSessionBootstrap starts the session's fan-out in the background unless
// one is already running for the session, and returns it -- nil when the
// session has nothing to connect. It returns as soon as the fan-out is
// planned; the caller's request is answered while the connects run. The
// caller's ID token is persisted first so the background closures can resolve
// it after the request context is gone (see storeIDTokenForSSO).
func (a *AggregatorServer) beginSessionBootstrap(sso ssoSession) *ssoBootstrap {
	musterIssuer := a.getMusterIssuer()

	// sso is a LogValuer: the structured attr renders truncated identifiers and
	// token lengths. Never hand it to a %v -- the raw struct holds the caller's
	// ID token and bearer.
	logging.InfoWithAttrs("Aggregator", "SSO: initSSOForSession called",
		slog.Any("session", sso),
		slog.String("musterIssuer", musterIssuer))

	if musterIssuer == "" {
		logging.Info("Aggregator", "SSO: initSSOForSession returning early: musterIssuer is empty")
		return nil
	}

	// Persist the caller's ID token into the OAuth-proxy store so the background
	// re-exchange/forwarding closures can resolve a subject after the request
	// context is gone. getIDTokenForForwarding runs on a detached
	// context.Background() and can only read the store; it is populated
	// otherwise only at fresh login (SessionCreationHandler) or on an upstream
	// refresh that returns an ID token (TokenRefreshHandler). A session that
	// reconnects after its login-time ID token expired (e.g. after a pod
	// restart) re-inits SSO here from the live request context but would
	// otherwise leave the store empty -- so every background re-exchange fails
	// with "no subject ID token available for re-exchange" and the fallback
	// refresher rotates the client's refresh token in a tight retry loop until
	// OAuth 2.1 reuse detection revokes the family and deauths the user
	// (giantswarm#37164). This persist only covers init time; between inits the
	// store is kept fresh by TokenRefreshHandler via the provider refresh.
	// storeIDTokenForSSO no-ops on empty/unparseable tokens.
	a.storeIDTokenForSSO(sso.sessionID, sso.userID, sso.tokens.IDToken)

	// A session whose first requests arrive together (initialize, then
	// tools/list a few milliseconds later) plans once.
	if b := a.sessionBootstrap(sso.sessionID); b != nil {
		return b
	}

	plan := a.planSessionBootstrap(sso)
	if plan.empty() {
		return nil
	}

	a.ssoBootstrapsMu.Lock()
	if b, ok := a.ssoBootstraps[sso.sessionID]; ok {
		a.ssoBootstrapsMu.Unlock()
		return b
	}
	b := newSSOBootstrap(sso.sessionID, plan.serverNames())
	if a.ssoBootstraps == nil {
		a.ssoBootstraps = make(map[string]*ssoBootstrap)
		a.ssoBootstrapped = make(map[string]time.Time)
	}
	a.ssoBootstraps[sso.sessionID] = b
	a.ssoBootstrapped[sso.sessionID] = b.started
	a.ssoBootstrapsMu.Unlock()

	logging.Info("Aggregator", "SSO: Connecting %d servers for session %s in the background (%d SSO, %d subject-scoped grants)",
		len(plan.sso)+len(plan.grants), logging.TruncateIdentifier(sso.sessionID), len(plan.sso), len(plan.grants))

	go a.runSessionBootstrap(b, sso, plan, musterIssuer) //nolint:gosec // G118: the fan-out must outlive the request that started it
	return b
}

// ssoServerResult is one server's share of a fan-out, for the summary.
type ssoServerResult struct {
	name    string
	outcome ssoConnectOutcome
	took    time.Duration
}

// runSessionBootstrap connects every server of the plan in parallel on a
// context bounded by initSSOTimeout, marks each server finished as its connect
// ends, and finally logs the fan-out's total duration, outcome counts and its
// slowest server -- the line that says what a session's start cost and where.
func (a *AggregatorServer) runSessionBootstrap(b *ssoBootstrap, sso ssoSession, plan sessionBootstrapPlan, musterIssuer string) {
	bgCtx, cancel := context.WithTimeout(context.Background(), initSSOTimeout)
	defer cancel()
	bgCtx = api.WithSubject(bgCtx, sso.userID)
	bgCtx = api.WithSessionID(bgCtx, sso.sessionID)
	bgCtx = server.ContextWithCallerTokens(bgCtx, sso.tokens)

	total := len(plan.sso) + len(plan.grants)
	results := make(chan ssoServerResult, total)
	var wg sync.WaitGroup
	connectOne := func(info *ServerInfo, connect func(context.Context, *ServerInfo) ssoConnectOutcome) {
		defer wg.Done()
		start := time.Now()
		outcome := connect(bgCtx, info)
		b.serverFinished(info.Name)
		results <- ssoServerResult{name: info.Name, outcome: outcome, took: time.Since(start)}
	}
	for _, info := range plan.sso {
		wg.Add(1)
		go connectOne(info, func(ctx context.Context, si *ServerInfo) ssoConnectOutcome {
			return a.connectSSOServer(ctx, si, musterIssuer)
		})
	}
	for _, info := range plan.grants {
		wg.Add(1)
		go connectOne(info, func(ctx context.Context, si *ServerInfo) ssoConnectOutcome {
			return a.adoptSubjectGrantForBootstrap(ctx, si, sso)
		})
	}

	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()
	timedOut := false
	select {
	case <-allDone:
	case <-time.After(initSSOTimeout + ssoBootstrapGrace):
		timedOut = true
	}

	// Release the waiters before the entry goes: a request that looked the
	// fan-out up a moment ago sees it finished, one arriving now finds none.
	b.finish()
	a.ssoBootstrapsMu.Lock()
	if a.ssoBootstraps[b.sessionID] == b {
		delete(a.ssoBootstraps, b.sessionID)
	}
	a.ssoBootstrapsMu.Unlock()

	var connected, failed, alreadyConnected int
	var slowest ssoServerResult
	summarised := 0
drain:
	for {
		select {
		case r := <-results:
			summarised++
			switch r.outcome {
			case ssoConnected:
				connected++
			case ssoAlreadyConnected:
				alreadyConnected++
			default:
				failed++
			}
			if r.took > slowest.took {
				slowest = r
			}
		default:
			break drain
		}
	}

	attrs := []slog.Attr{
		slog.String("sessionID", logging.TruncateIdentifier(sso.sessionID)),
		slog.String("userID", logging.TruncateIdentifier(sso.userID)),
		slog.Int("servers", total),
		slog.Int("connected", connected),
		slog.Int("alreadyConnected", alreadyConnected),
		slog.Int("failed", failed),
		slog.Int("unfinished", total-summarised),
		slog.Float64("duration_s", time.Since(b.started).Seconds()),
		slog.String("slowestServer", slowest.name),
		slog.Float64("slowest_s", slowest.took.Seconds()),
		slog.Bool("timedOut", timedOut),
	}
	if timedOut {
		logging.WarnWithAttrs("Aggregator", "SSO: fan-out finished with connects still running", attrs...)
		return
	}
	logging.InfoWithAttrs("Aggregator", "SSO: fan-out finished", attrs...)
}

// connectSSOServer connects one SSO server for the fan-out: the test seam
// ssoConnect when set, establishSSOConnection otherwise.
func (a *AggregatorServer) connectSSOServer(ctx context.Context, info *ServerInfo, musterIssuer string) ssoConnectOutcome {
	if a.ssoConnect != nil {
		return a.ssoConnect(ctx, info, musterIssuer)
	}
	return a.establishSSOConnection(ctx, info, musterIssuer)
}

// adoptSubjectGrantForBootstrap connects the session to a subject-scoped
// server with the person's grant, as adoptSubjectGrant does on a tool call, and
// maps its answer onto the fan-out's outcomes. No grant is not a failure: the
// server simply stays under auth_required for the session.
func (a *AggregatorServer) adoptSubjectGrantForBootstrap(ctx context.Context, info *ServerInfo, sso ssoSession) ssoConnectOutcome {
	adopted, err := a.adoptSubjectGrant(ctx, info, sso.sessionID, sso.userID)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Could not connect with the person's existing grant",
			slog.String("server", info.Name),
			slog.String("sessionID", logging.TruncateIdentifier(sso.sessionID)),
			slog.String("error", err.Error()))
		return ssoConnectFailed
	}
	if adopted {
		return ssoConnected
	}
	return ssoAlreadyConnected
}

// ssoBootstrappedRetention is how long a session is remembered as having
// started its fan-out (ssoBootstrapped). It outlives the tracker's longest
// failure backoff, so a session with no connected server clears the person's
// failures once and then retries on the backoff schedule alone; a session
// idle for longer is treated as new again.
const ssoBootstrappedRetention = 2 * ssoBackoffMaxTTL

// sessionBootstrapped reports whether the session started a fan-out within
// ssoBootstrappedRetention. Entries past the retention are dropped as a side
// effect, which bounds the record to the sessions of the last hour.
func (a *AggregatorServer) sessionBootstrapped(sessionID string) bool {
	a.ssoBootstrapsMu.Lock()
	defer a.ssoBootstrapsMu.Unlock()
	cutoff := time.Now().Add(-ssoBootstrappedRetention)
	for id, at := range a.ssoBootstrapped {
		if at.Before(cutoff) {
			delete(a.ssoBootstrapped, id)
		}
	}
	_, ok := a.ssoBootstrapped[sessionID]
	return ok
}

// sessionBootstrap returns the fan-out in flight for the session, or nil.
func (a *AggregatorServer) sessionBootstrap(sessionID string) *ssoBootstrap {
	if sessionID == "" {
		return nil
	}
	a.ssoBootstrapsMu.Lock()
	defer a.ssoBootstrapsMu.Unlock()
	return a.ssoBootstraps[sessionID]
}

// awaitSessionBootstrap blocks until the session's fan-out, if one is in
// flight, has finished or ctx is done, and reports how long it waited. It is
// what a listing pays so that a session's first list_tools shows the servers
// its start connects instead of whichever had finished by then.
func (a *AggregatorServer) awaitSessionBootstrap(ctx context.Context, sessionID string) time.Duration {
	b := a.sessionBootstrap(sessionID)
	if b == nil {
		return 0
	}
	waited := b.wait(ctx)
	if waited > 0 {
		logging.InfoWithAttrs("Aggregator", "SSO: request waited for the session's fan-out",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.Float64("waited_s", waited.Seconds()),
			slog.Bool("finished", ctx.Err() == nil))
	}
	return waited
}

// awaitServerBootstrap blocks until the session's fan-out has finished with
// the server, if it covers it, or ctx is done, and reports how long it waited.
// A call to one server waits for that server's connect alone; the other
// connects keep running.
func (a *AggregatorServer) awaitServerBootstrap(ctx context.Context, sessionID, serverName string) time.Duration {
	b := a.sessionBootstrap(sessionID)
	if b == nil {
		return 0
	}
	waited := b.waitServer(ctx, serverName)
	if waited > 0 {
		logging.InfoWithAttrs("Aggregator", "SSO: request waited for the server's connect",
			slog.String("server", serverName),
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.Float64("waited_s", waited.Seconds()),
			slog.Bool("finished", ctx.Err() == nil))
	}
	return waited
}

// awaitToolOwnersBootstrap holds a call for an exposed tool name until the
// session's fan-out has finished with every server that could own the name --
// the servers whose prefix it carries and the members of the family whose name
// space it lies in, narrowed to the one member the call's instance argument
// selects. The name cannot be resolved before those servers have connected
// (their tools enter the session's capability store with the connection), so
// without this the call would fail with "tool not found" while the connect it
// needs is a few hundred milliseconds from done. Servers the fan-out does not
// cover, and names no server could own (core tools, unknown names), wait for
// nothing.
func (a *AggregatorServer) awaitToolOwnersBootstrap(ctx context.Context, sessionID, exposedName string, args map[string]any) time.Duration {
	b := a.sessionBootstrap(sessionID)
	if b == nil {
		return 0
	}
	owners := a.registry.ServersInNameSpaceOf(exposedName, args)
	var waited time.Duration
	for _, owner := range owners {
		if !b.covers(owner) {
			continue
		}
		waited += b.waitServer(ctx, owner)
		if ctx.Err() != nil {
			break
		}
	}
	if waited > 0 {
		logging.InfoWithAttrs("Aggregator", "SSO: tool call waited for its server's connect",
			slog.String("tool", exposedName),
			slog.Any("servers", owners),
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.Float64("waited_s", waited.Seconds()),
			slog.Bool("finished", ctx.Err() == nil))
	}
	return waited
}
