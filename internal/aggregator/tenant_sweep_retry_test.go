package aggregator

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// sweepPauses hands each pause of muster's sweep retries to the test, which
// lets the attempt run with resume.
type sweepPauses struct {
	paused chan time.Duration
	resume chan struct{}
}

// newSweepPauses returns pauses whose wait hands each pause to the test.
func newSweepPauses() *sweepPauses {
	return &sweepPauses{paused: make(chan time.Duration), resume: make(chan struct{})}
}

// wait is a tenantSweepRetries wait: it hands the pause to the test and
// returns when the test resumes it or muster stops.
func (p *sweepPauses) wait(ctx context.Context, d time.Duration) error {
	select {
	case p.paused <- d:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-p.resume:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pauseSweepRetries makes the sweep retries of f's muster wait for the test
// instead of sleeping.
func (f *tenantFixture) pauseSweepRetries() *sweepPauses {
	p := newSweepPauses()
	f.am.aggregatorServer.tenantSweeps.wait = p.wait
	return p
}

// next returns the backoff of the retry's next pause.
func (p *sweepPauses) next(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-p.paused:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("no sweep retry is waiting")
		return 0
	}
}

// settled waits until no sweep retry of f's muster runs.
func (f *tenantFixture) settled() {
	f.am.aggregatorServer.tenantSweeps.wg.Wait()
}

// stop ends f's muster process: its sweep retries end, as when the pod goes.
func (f *tenantFixture) stop() {
	f.am.aggregatorServer.tenantSweeps.stop()
}

// sweepAtStart starts the sweep at start of f's muster, as Start does with
// MCPServersListed set: it waits for listed to close.
func (f *tenantFixture) sweepAtStart(listed <-chan struct{}) {
	a := f.am.aggregatorServer
	a.tenantSweeps.start(func(ctx context.Context) {
		a.sweepRecordedTenantsAtStart(ctx, listed)
	})
}

// listedAlready returns a closed MCPServersListed: the objects are listed.
func listedAlready() <-chan struct{} {
	listed := make(chan struct{})
	close(listed)
	return listed
}

// The sweep of a deleted organization namespace that fails while the store
// does not answer is retried, with a growing pause and no cap, until it
// succeeds: the namespace is never adopted again (giantswarm/muster#1766).
// The sign-ins of muster's namespace stay.
func TestTenantSignIns_FailedSweepIsRetried(t *testing.T) {
	const (
		tenant = "org-b/fixture"
		own    = "github"
	)
	f := newTenantFixture(t, "valkey")
	pauses := f.pauseSweepRetries()
	f.define(tenant, "uid-1")
	f.define(own, "uid-own")
	require.NoError(t, f.register(tenant, "uid-1", tenantTestIssuer))
	require.NoError(t, f.register(own, "uid-own", tenantTestIssuer))
	f.signIn("bob", tenant, tenantTestIssuer)
	f.signIn("alice", own, tenantTestIssuer)

	f.valkey.SetError("ERR the store is unavailable")
	f.remove(tenant)

	var backoffs []time.Duration
	for range 5 {
		backoffs = append(backoffs, pauses.next(t))
		if len(backoffs) == 5 {
			f.valkey.SetError("")
		}
		pauses.resume <- struct{}{}
	}
	f.settled()

	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}, backoffs)
	grant, mark := f.signedIn("bob", tenant, tenantTestIssuer)
	assert.False(t, grant, "the retry forgets the deleted namespace's grant")
	assert.False(t, mark)
	owners, err := f.owners.Owners(context.Background(), "org-b")
	require.NoError(t, err)
	assert.Empty(t, owners)

	grant, mark = f.signedIn("alice", own, tenantTestIssuer)
	assert.True(t, grant, "a server of muster's namespace keeps its sign-in")
	assert.True(t, mark)
}

// A namespace created again with the same name before the retry of the old
// one's sweep runs keeps the sign-ins of its new server: the retry acts on
// the servers that exist when it runs.
func TestTenantSignIns_RetriedSweepSparesARecreatedNamespace(t *testing.T) {
	const key = "org-b/fixture"
	f := newTenantFixture(t, "valkey")
	pauses := f.pauseSweepRetries()
	f.define(key, "uid-1")
	require.NoError(t, f.register(key, "uid-1", tenantTestIssuer))
	f.signIn("bob", key, tenantTestIssuer)

	f.valkey.SetError("ERR the store is unavailable")
	f.remove(key)
	pauses.next(t)

	f.valkey.SetError("")
	f.define(key, "uid-2")
	require.NoError(t, f.register(key, "uid-2", tenantTestIssuer))
	grant, _ := f.signedIn("bob", key, tenantTestIssuer)
	require.False(t, grant, "the new server's adoption swept the old one's grant")
	f.signIn("carol", key, tenantTestIssuer)

	pauses.resume <- struct{}{}
	f.settled()

	grant, mark := f.signedIn("carol", key, tenantTestIssuer)
	assert.True(t, grant, "the retry leaves the new server's sign-in")
	assert.True(t, mark)
	owners, err := f.owners.Owners(context.Background(), "org-b")
	require.NoError(t, err)
	assert.Contains(t, owners, "uid-2")
}

var errStoreUnavailable = errors.New("the store is unavailable")

// Stopping muster ends a waiting retry before its attempt, and a failure
// after the stop schedules nothing: no retry, no pause, no sweep.
func TestTenantSweepRetries_StopEndsThem(t *testing.T) {
	pauses := newSweepPauses()
	r := &tenantSweepRetries{wait: pauses.wait}
	sweeps := 0
	sweep := func(context.Context) error {
		sweeps++
		return nil
	}
	r.schedule("org-a", "org-a/fixture", errStoreUnavailable, sweep)
	pauses.next(t)
	r.stop()
	assert.Zero(t, sweeps, "the retry stopped in its pause")

	waits := 0
	r.wait = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	r.schedule("org-b", "org-b/fixture", errStoreUnavailable, sweep)
	r.mu.Lock()
	assert.Empty(t, r.pending, "no retry is scheduled after the stop")
	r.mu.Unlock()
	r.wg.Wait()
	assert.Zero(t, waits)
	assert.Zero(t, sweeps)
}

// A sweep of a namespace that fails while the namespace's retry makes an
// attempt (which may have read the state before that failure) makes the
// retry run once more after the attempt succeeds, instead of a second retry.
func TestTenantSweepRetries_AFailureDuringAnAttemptRunsItOnceMore(t *testing.T) {
	r := &tenantSweepRetries{wait: func(context.Context, time.Duration) error { return nil }}
	attempting := make(chan struct{})
	release := make(chan struct{})
	sweeps := 0
	r.schedule("org-a", "org-a/github", errStoreUnavailable, func(context.Context) error {
		sweeps++
		if sweeps == 1 {
			close(attempting)
			<-release
		}
		return nil
	})
	select {
	case <-attempting:
	case <-time.After(10 * time.Second):
		t.Fatal("the retry made no attempt")
	}

	r.schedule("org-a", "org-a/miro", errStoreUnavailable, func(context.Context) error {
		t.Error("a second retry of the namespace runs")
		return nil
	})
	close(release)
	r.wg.Wait()

	assert.Equal(t, 2, sweeps, "the retry swept once more")
	r.mu.Lock()
	assert.Empty(t, r.pending)
	r.mu.Unlock()
}

// muster restarts while the store does not answer, after an organization
// namespace was deleted: its retry ended with the old process. When the store
// answers again, the sweep at start forgets the namespace's grant, mark and
// owner record (giantswarm/muster#1772). The live namespace and muster's
// namespace keep their sign-ins.
func TestTenantSignIns_RestartDuringAnOutageSweepsTheDeletedNamespace(t *testing.T) {
	const (
		deleted = "org-b/fixture"
		live    = "org-a/github"
		own     = "github"
	)
	f := newTenantFixture(t, "valkey")
	pauses := f.pauseSweepRetries()
	f.define(deleted, "uid-b")
	f.define(live, "uid-a")
	f.define(own, "uid-own")
	require.NoError(t, f.register(deleted, "uid-b", tenantTestIssuer))
	require.NoError(t, f.register(live, "uid-a", tenantTestIssuer))
	require.NoError(t, f.register(own, "uid-own", tenantTestIssuer))
	f.signIn("bob", deleted, tenantTestIssuer)
	f.signIn("alice", live, tenantTestIssuer)
	f.signIn("carol", own, tenantTestIssuer)

	f.valkey.SetError("ERR the store is unavailable")
	f.remove(deleted)
	pauses.next(t)
	f.stop()

	f.restart()
	pauses = f.pauseSweepRetries()
	f.sweepAtStart(listedAlready())
	var backoffs []time.Duration
	for range 2 {
		backoffs = append(backoffs, pauses.next(t))
		if len(backoffs) == 2 {
			f.valkey.SetError("")
		}
		pauses.resume <- struct{}{}
	}
	f.settled()

	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, backoffs, "the store is asked again with the retries' backoff")
	grant, mark := f.signedIn("bob", deleted, tenantTestIssuer)
	assert.False(t, grant, "the sweep at start forgets the deleted namespace's grant")
	assert.False(t, mark)
	owners, err := f.owners.Owners(context.Background(), "org-b")
	require.NoError(t, err)
	assert.Empty(t, owners)
	namespaces, err := f.owners.Namespaces(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a"}, namespaces)

	grant, mark = f.signedIn("alice", live, tenantTestIssuer)
	assert.True(t, grant, "a live tenant server keeps its sign-in")
	assert.True(t, mark)
	grant, mark = f.signedIn("carol", own, tenantTestIssuer)
	assert.True(t, grant, "a server of muster's namespace keeps its sign-in")
	assert.True(t, mark)
}

// The sweep at start waits until the MCPServer objects are listed: before
// that, a listing shows no live server, and a sweep would erase the sign-ins
// of every tenant. Once they are listed it sweeps the namespace that went
// while muster was down, and only that one.
func TestTenantSignIns_SweepAtStartWaitsForTheListing(t *testing.T) {
	const (
		deleted = "org-b/fixture"
		live    = "org-a/github"
	)
	f := newTenantFixture(t, "valkey")
	f.define(deleted, "uid-b")
	f.define(live, "uid-a")
	require.NoError(t, f.register(deleted, "uid-b", tenantTestIssuer))
	require.NoError(t, f.register(live, "uid-a", tenantTestIssuer))
	f.signIn("bob", deleted, tenantTestIssuer)
	f.signIn("alice", live, tenantTestIssuer)

	// muster is down while org-b goes; it starts before its informer lists
	// the objects that are left.
	delete(f.definitions.servers, deleted)
	listing := f.definitions.servers
	f.definitions.servers = map[string]api.MCPServerInfo{}
	f.restart()
	listed := make(chan struct{})
	f.sweepAtStart(listed)

	assert.Never(t, func() bool {
		owners, err := f.owners.Owners(context.Background(), "org-a")
		return err != nil || len(owners) == 0
	}, 300*time.Millisecond, 10*time.Millisecond, "the sweep at start ran before the listing")

	f.definitions.servers = listing
	close(listed)
	f.settled()

	grant, mark := f.signedIn("alice", live, tenantTestIssuer)
	assert.True(t, grant, "the live tenant server keeps its sign-in")
	assert.True(t, mark)
	grant, mark = f.signedIn("bob", deleted, tenantTestIssuer)
	assert.False(t, grant, "the namespace that went while muster was down is swept")
	assert.False(t, mark)
}

// A namespace whose sweep at start fails (here the MCPServer listing) goes to
// the run-time retries: it waits 1 s and is swept again until it succeeds.
func TestTenantSignIns_FailedSweepAtStartIsRetried(t *testing.T) {
	const deleted = "org-b/fixture"
	f := newTenantFixture(t, "valkey")
	f.define(deleted, "uid-b")
	require.NoError(t, f.register(deleted, "uid-b", tenantTestIssuer))
	f.signIn("bob", deleted, tenantTestIssuer)

	delete(f.definitions.servers, deleted)
	f.definitions.err = errStoreUnavailable
	f.restart()
	pauses := f.pauseSweepRetries()
	f.sweepAtStart(listedAlready())

	assert.Equal(t, time.Second, pauses.next(t))
	r := &f.am.aggregatorServer.tenantSweeps
	r.mu.Lock()
	assert.Contains(t, r.pending, "org-b", "the namespace waits for a run-time retry")
	r.mu.Unlock()
	f.definitions.err = nil
	pauses.resume <- struct{}{}
	f.settled()

	grant, mark := f.signedIn("bob", deleted, tenantTestIssuer)
	assert.False(t, grant, "the retry forgets the deleted namespace's grant")
	assert.False(t, mark)
}

// A namespace that keeps some of its servers loses at start only what the
// servers that went while muster was down alone used: their issuer keys and
// records. The servers left keep their sign-ins.
func TestTenantSignIns_SweepAtStartKeepsTheServersLeft(t *testing.T) {
	const (
		github = "org-a/github"
		miro   = "org-a/miro"
		other  = "https://other.example.com"
	)
	f := newTenantFixture(t, "valkey")
	f.define(github, "uid-g")
	f.define(miro, "uid-m")
	require.NoError(t, f.register(github, "uid-g", tenantTestIssuer))
	require.NoError(t, f.register(miro, "uid-m", other))
	f.signIn("alice", github, tenantTestIssuer)
	f.signIn("alice", miro, other)

	delete(f.definitions.servers, miro)
	f.restart()
	f.sweepAtStart(listedAlready())
	f.settled()

	grant, mark := f.signedIn("alice", github, tenantTestIssuer)
	assert.True(t, grant, "the server left keeps its sign-in")
	assert.True(t, mark)
	grant, mark = f.signedIn("alice", miro, other)
	assert.False(t, grant, "the issuer only the gone server named is forgotten")
	assert.False(t, mark)
	owners, err := f.owners.Owners(context.Background(), "org-a")
	require.NoError(t, err)
	assert.Equal(t, []string{"uid-g"}, slices.Collect(maps.Keys(owners)))
}

// A record the namespace index lost (its write failed between the record and
// the index, or a muster without the index wrote it) is indexed again by the
// server's next registration, so the sweep at start finds it.
func TestTenantSignIns_RegistrationIndexesTheRecord(t *testing.T) {
	const key = "org-a/github"
	f := newTenantFixture(t, "valkey")
	f.define(key, "uid-g")
	require.NoError(t, f.register(key, "uid-g", tenantTestIssuer))
	_, err := f.owners.Namespaces(context.Background()) // the earlier records are indexed
	require.NoError(t, err)
	_, err = f.valkey.SRem("muster:tenant_owner_namespaces", "org-a")
	require.NoError(t, err)

	f.restart()
	require.NoError(t, f.register(key, "uid-g", tenantTestIssuer))

	namespaces, err := f.owners.Namespaces(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"org-a"}, namespaces)
}
