package aggregator

import (
	"context"
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

// pauseSweepRetries makes the sweep retries of f's muster wait for the test
// instead of sleeping.
func (f *tenantFixture) pauseSweepRetries() *sweepPauses {
	p := &sweepPauses{paused: make(chan time.Duration), resume: make(chan struct{})}
	f.am.aggregatorServer.tenantSweeps.wait = func(ctx context.Context, d time.Duration) error {
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

// Stopping muster ends a waiting retry, and no retry starts afterwards.
func TestTenantSweepRetries_StopEndsThem(t *testing.T) {
	var r tenantSweepRetries
	attempts := 0
	r.schedule("org-a", func(context.Context) error {
		attempts++
		return nil
	})
	r.stop()
	r.schedule("org-b", func(context.Context) error {
		attempts++
		return nil
	})
	r.wg.Wait()
	assert.Zero(t, attempts, "the retry waited its first pause when muster stopped")
}
