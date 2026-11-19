package aggregator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/giantswarm/mcp-oauth/providers"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"golang.org/x/oauth2"
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
// person's subject-scoped grants. It is started by the person's sign-in or by
// the session's first request, and finishes in the background: the sign-in
// gets its token and the request is answered meanwhile (#1226). A request
// that needs one server waits for that server's connect alone (waitServer);
// one that needs the whole catalogue waits for the fan-out (wait). Both waits
// end with the caller's context.
//
// Code that reads a session's tools or per-server auth state can run while
// the fan-out is still connecting, so it calls one of the await helpers
// first. The readers that do:
//
//   - CallToolInternal: awaitToolOwnersBootstrap (the servers that could own
//     the tool name)
//   - getOrCreateClientForToolCall: awaitServerBootstrap
//   - ListToolsForContext, ListResourcesForContext, ListPromptsForContext:
//     awaitSessionBootstrap
//   - MissingToolsForSession (workflow availability): awaitSessionBootstrap
//   - handleAuthLogin (core_auth_login): awaitServerBootstrap
//   - handleAuthStatusResource (auth://status): awaitGrantAdoptions for the
//     servers connected with the person's grant; an SSO server still
//     connecting reads sso_pending (connecting), without a wait
//
// A new reader of that state joins this list.
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

// connecting reports whether the fan-out covers the server and its connect
// has not finished yet.
func (b *ssoBootstrap) connecting(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.servers[name]
	if !ok {
		return false
	}
	select {
	case <-ch:
		return false
	default:
		return true
	}
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
	// The fan-out connects only servers the person may see, judged with the
	// session's own ID token.
	view := api.CallerVisibility(sso.connectContext(context.Background()))
	var skippedNotAuthRequired, skippedNotSSO, skippedPriorFailure, skippedNotVisible, skippedForwarding int
	for _, info := range servers {
		if !view.Sees(info.Name) {
			skippedNotVisible++
			continue
		}
		if !info.RequiresSessionAuth() {
			skippedNotAuthRequired++
			continue
		}
		if sso.delegated != nil && delegatedForwardRefusal(info) != nil {
			skippedForwarding++
			continue
		}
		if !ShouldUseTokenExchange(info) && !ShouldUseTokenForwarding(info) {
			skippedNotSSO++
			if a.sessionMayAdoptGrant(info, sso) {
				plan.grants = append(plan.grants, info)
			}
			continue
		}
		if a.ssoTracker != nil {
			if failure, ok := a.ssoTracker.activeFailure(sso.userID, info.Name); ok {
				logging.Info("Aggregator", "SSO: skipping %s for user %s until %s after %d %s failure(s): %s",
					info.Name, logging.TruncateIdentifier(sso.userID),
					failure.failedAt.Add(failure.backoff()).UTC().Format(time.RFC3339),
					failure.failureCount, failure.kind, failure.reason)
				skippedPriorFailure++
				continue
			}
		}
		plan.sso = append(plan.sso, info)
	}

	logging.Info("Aggregator", "SSO: initSSOForSession filter results: total=%d, pending=%d, grants=%d, skippedNotAuthRequired=%d, skippedNotSSO=%d, skippedPriorFailure=%d, skippedNotVisible=%d, skippedForwardingForDelegated=%d",
		len(servers), len(plan.sso), len(plan.grants), skippedNotAuthRequired, skippedNotSSO, skippedPriorFailure, skippedNotVisible, skippedForwarding)
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

// onSessionCreated starts a new session's SSO fan-out when a person signs in.
// mcp-oauth calls it inside ExchangeAuthorizationCode, before the token
// response is written, so it does not wait for the connects: the token
// reaches the client at once, and the session's first requests wait for the
// servers they need, as on the request path (#1226). A slow server must not
// hold the token past the timeout of a proxy in front of muster.
func (a *AggregatorServer) onSessionCreated(userID, familyID string, token *oauth2.Token) {
	idToken := oauthserver.ExtractIDToken(token)
	logging.InfoWithAttrs("Aggregator", "SSO: SessionCreationHandler fired",
		slog.String("userID", logging.TruncateIdentifier(userID)),
		slog.String("familyID", logging.TruncateIdentifier(familyID)),
		slog.Bool("hasIDToken", idToken != ""),
		slog.Int("idTokenLen", len(idToken)))
	// A sign-in brings a new subject token: failures recorded against the
	// previous one are not this session's, as on a session's first fan-out.
	if a.ssoTracker != nil && userID != "" {
		a.ssoTracker.ClearAllSSOFailed(userID)
	}
	// beginSessionBootstrap persists idToken into the OAuth-proxy store
	// itself, so no separate storeIDTokenForSSO call is needed here.
	a.beginSessionBootstrap(ssoSession{userID: userID, sessionID: familyID, tokens: server.CallerTokens{IDToken: idToken}})
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
	}
	if a.ssoBootstrapped == nil {
		a.ssoBootstrapped = make(map[string]ssoBootstrapMark)
	}
	a.ssoBootstraps[sso.sessionID] = b
	a.ssoBootstrapped[sso.sessionID] = ssoBootstrapMark{started: b.started}
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
	bgCtx = sso.connectContext(bgCtx)

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

// scheduleSSORetry connects the session's SSO server again after delay, on
// muster's own initiative: a fresh token the backend refused is retried
// without waiting for the person's next request. The retry carries the
// session's subject and caller tokens, so it forwards the same token; once
// that token is older than ssoFreshTokenWindow, a further 401 is an auth
// failure and the retries end. It is skipped when muster is stopping, the
// session ended, or a fan-out of the session is connecting the server.
//
// The session is tracked here, not only by its first tools/list or tool call:
// a session that has done neither (a client that initialized and reads
// auth://status) is alive too, and its teardown (UntrackOAuth) is what ends
// the retries.
func (a *AggregatorServer) scheduleSSORetry(ctx context.Context, serverName, musterIssuer string, delay time.Duration) {
	sub := getUserSubjectFromContext(ctx)
	sessionID := getSessionIDFromContext(ctx)
	tokens := server.CallerTokensFromContext(ctx)
	caller, delegated := api.DelegatedCallerFromContext(ctx)
	if a.subjectSessions != nil {
		a.subjectSessions.TrackOAuth(sub, sessionID)
	}
	logging.Info("Aggregator", "SSO: retrying %s for user %s in %s: fresh token refused",
		serverName, logging.TruncateIdentifier(sub), delay)

	retry := func() {
		if a.ctx != nil && a.ctx.Err() != nil {
			return
		}
		if a.subjectSessions != nil && a.subjectSessions.OAuthSubject(sessionID) == "" {
			logging.Info("Aggregator", "SSO: retry of %s skipped, session %s ended",
				serverName, logging.TruncateIdentifier(sessionID))
			return
		}
		if b := a.sessionBootstrap(sessionID); b != nil && b.connecting(serverName) {
			return
		}
		info, ok := a.registry.GetServerInfo(serverName)
		if !ok {
			return
		}
		retryCtx, cancel := context.WithTimeout(context.Background(), initSSOTimeout)
		defer cancel()
		retryCtx = api.WithSubject(retryCtx, sub)
		retryCtx = api.WithSessionID(retryCtx, sessionID)
		retryCtx = server.ContextWithCallerTokens(retryCtx, tokens)
		if delegated {
			retryCtx = api.WithDelegatedCaller(retryCtx, caller)
		}
		a.establishSSOConnection(retryCtx, info, musterIssuer)
	}
	a.retryAfter(delay, retry)
}

// retryAfter runs a retry muster schedules on its own initiative after delay:
// on a timer, or through the test seam ssoRetryAfter when set.
func (a *AggregatorServer) retryAfter(delay time.Duration, retry func()) {
	if a.ssoRetryAfter != nil {
		a.ssoRetryAfter(delay, retry)
		return
	}
	time.AfterFunc(delay, retry)
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

// ssoBootstrapMark is what a pod remembers of a session's last fan-out: when
// it started, and whether the session held auth state since then -- this pod
// connected the session to a server (in the fan-out, a retry or a tool call),
// or a request of the session found its shared auth state alive (a connect on
// another replica). The auth state itself is shared (Valkey); the mark is
// this pod's.
type ssoBootstrapMark struct {
	started       time.Time
	authenticated bool
}

// markSessionAuthenticated records that the session holds auth state since
// its last fan-out started. A session with no mark is left alone: it has no
// fan-out to start again.
func (a *AggregatorServer) markSessionAuthenticated(sessionID string) {
	a.ssoBootstrapsMu.Lock()
	defer a.ssoBootstrapsMu.Unlock()
	if m, ok := a.ssoBootstrapped[sessionID]; ok && !m.authenticated {
		m.authenticated = true
		a.ssoBootstrapped[sessionID] = m
	}
}

// markSessionAuthenticatedTo records in the shared auth store that the
// session authenticated to the server, and in this pod's fan-out mark that
// the session holds auth state. Every connect that authenticates a session
// calls it.
func (a *AggregatorServer) markSessionAuthenticatedTo(ctx context.Context, sessionID, serverName string) {
	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverName); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}
	a.markSessionAuthenticated(sessionID)
}

// claimSessionBootstrap reports whether a request of a session with no auth
// state may clear the person's SSO failures and start the session's fan-out:
// the session started no fan-out within ssoBootstrappedRetention, or it held
// auth state since its last fan-out started (see ssoBootstrapMark) and that
// state has ended (the resource bearer expired, a logout, a revoked refresh)
// while the session ID stayed the same. A session that held no auth state
// since its last fan-out (every connect failed) is not claimed: its requests retry on the tracker's backoff,
// not on each request. A claim of an ended session consumes the mark's
// authenticated flag, so of concurrent requests one claims it. Entries past
// the retention are dropped as a side effect, which bounds the record to the
// sessions of the last hour.
func (a *AggregatorServer) claimSessionBootstrap(sessionID string) bool {
	a.ssoBootstrapsMu.Lock()
	defer a.ssoBootstrapsMu.Unlock()
	cutoff := time.Now().Add(-ssoBootstrappedRetention)
	for id, m := range a.ssoBootstrapped {
		if m.started.Before(cutoff) {
			delete(a.ssoBootstrapped, id)
		}
	}
	m, ok := a.ssoBootstrapped[sessionID]
	if !ok {
		return true
	}
	if !m.authenticated {
		return false
	}
	m.authenticated = false
	a.ssoBootstrapped[sessionID] = m
	return true
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

// connectToolOwnersOnCall connects the session, on a call whose tool name
// resolves to nothing the session holds, to the servers that could own the
// name, and reports how many it connected: the SSO servers among the owners
// with no pooled connection for the session, then the person's subject-scoped
// grants. A session's fan-out does not always cover such an SSO server -- one
// registered after the session started, or one whose pending slot another
// session of the person holds (the slot is the person's, the connection the
// session's) -- and without this the call would answer "tool not found" for a
// tool the session's identity reaches. An SSO server in failure backoff for
// the person is left to its retry schedule.
func (a *AggregatorServer) connectToolOwnersOnCall(ctx context.Context, sessionID, sub, exposedName string, args map[string]any) int {
	if sessionID == "" || sub == "" {
		return 0
	}
	connected := 0
	if musterIssuer := a.getMusterIssuer(); musterIssuer != "" && a.connPool != nil {
		for _, owner := range a.registry.ServersInNameSpaceOf(exposedName, args) {
			info, ok := a.registry.GetServerInfo(owner)
			if !ok || !info.RequiresSessionAuth() || (!ShouldUseTokenExchange(info) && !ShouldUseTokenForwarding(info)) {
				continue
			}
			if _, pooled := a.connPool.Get(sessionID, owner); pooled {
				continue
			}
			if a.ssoTracker != nil {
				if _, failing := a.ssoTracker.activeFailure(sub, owner); failing {
					continue
				}
			}
			if a.connectSSOServer(ctx, info, musterIssuer) == ssoConnected {
				logging.InfoWithAttrs("Aggregator", "SSO: tool call connected the session to its server",
					slog.String("tool", exposedName),
					slog.String("server", owner),
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
				connected++
			}
		}
	}
	return connected + a.adoptSubjectGrants(ctx, sessionID, sub)
}

// onRequestAuthenticated runs on every authenticated request. It extends the
// session's capability store TTL and, if the session is new or has expired
// (e.g. user returns after inactivity, or Valkey was restarted), starts its
// SSO fan-out in the background and answers the request meanwhile: tools/list
// returns the meta-tools, which need none of the connections, and a call for
// a server still connecting waits for that server alone (#1226).
// Also detects broken upstream refresh chains: when authAlive is true but
// the ID token has disappeared, SSO connections are evicted to stop the
// mcp-go retry loop from spamming errors with expired tokens.
func (a *AggregatorServer) onRequestAuthenticated(ctx context.Context, sessionID string) {
	if a.capabilityStore == nil {
		return
	}
	var authAlive bool
	if a.authStore != nil {
		authAlive, _ = a.authStore.Touch(ctx, sessionID)
	}
	_, _ = a.capabilityStore.Touch(ctx, sessionID)

	sso := ssoSessionFromContext(ctx, sessionID)
	a.bindBearerSession(ctx, sso)

	logging.InfoWithAttrs("Aggregator", "SSO: onAuthenticated callback",
		slog.Any("session", sso),
		slog.Bool("authAlive", authAlive))

	if authAlive {
		if !sso.canBootstrapSSO() {
			if sso.tokenSource == providers.TokenSourceTrustedIssuer {
				return
			}
			// No usable subject token: the upstream credential is gone.
			// Evict stale SSO connections to stop mcp-go's infinite
			// 1-second retry loop.
			a.handleUpstreamRefreshFailure(sessionID, sso.userID, "onAuthenticated: no usable subject token for active session")
			return
		}
		// After a pod restart the auth store survives in Valkey but the
		// in-memory conn pool is empty. Trigger SSO re-init if any
		// token-exchange server is registered but has no pooled client.
		// Failure backoff is intentionally NOT cleared here: the tracker is
		// in-memory, so after a restart there are no stale failures, and
		// clearing it on every request would retry a persistently failing
		// exchange per-request instead of on the backoff schedule.
		// A fan-out already running for the session (a sign-in's, whose
		// fast servers made the session live) covers the misses.
		if a.getMusterIssuer() != "" && a.sessionBootstrap(sessionID) == nil &&
			a.ssoPoolMissNeedingInitOf(sso.userID, sessionID, seesFunc(api.CallerVisibility(sso.connectContext(context.Background())))) {
			logging.InfoWithAttrs("Aggregator", "SSO: pool miss on live session, triggering SSO re-init",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
			a.beginSessionBootstrap(sso)
		}
		// The shared auth state is alive: whichever replica connected the
		// session, this pod starts the fan-out again once it ends.
		a.markSessionAuthenticated(sessionID)
		return
	}

	if !sso.canBootstrapSSO() {
		logging.InfoWithAttrs("Aggregator", "SSO: skipping bootstrap, no usable subject token",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
		return
	}

	// A session's first fan-out clears the person's prior SSO failures so
	// all servers are retried: servers that failed during a previous init
	// (e.g. before a pod restart or after a transient token-storage error)
	// would otherwise be skipped for ssoTrackerFailureTTL although the
	// cause may be gone. Only the first: a session whose every connect
	// failed has no connected server and lands here on each request, and
	// clearing then would retry the failing exchange per request instead
	// of on the tracker's backoff -- and, with the fan-out in the
	// background, show the server as pending on every status read.
	// A session that held auth state which then ended (its forwarded ID
	// token expired and ended the session, a logout) lands here too, with
	// the same session ID: the session ID of a forwarded token is derived
	// from its principal, not its bytes. It starts once more, as a new
	// session does.
	if !a.claimSessionBootstrap(sessionID) {
		return
	}
	if a.ssoTracker != nil && sso.userID != "" {
		a.ssoTracker.ClearAllSSOFailed(sso.userID)
	}

	a.beginSessionBootstrap(sso)
}
