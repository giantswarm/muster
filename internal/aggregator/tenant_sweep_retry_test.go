package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
