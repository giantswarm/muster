package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
)

// completionReplica is an aggregator with its own MCP server and sessions, on a
// completion bus shared with the others.
type completionReplica struct {
	a       *AggregatorServer
	session *legacySession
}

func newCompletionReplica(t *testing.T, bus approval.Completions, transportID string) completionReplica {
	t.Helper()
	srv := mcpserver.NewMCPServer("muster", "test")
	session := newLegacySession(transportID, urlElicitationCapabilities())
	require.NoError(t, srv.RegisterSession(t.Context(), session))
	a := &AggregatorServer{mcpServer: srv, completions: bus, ctx: t.Context()}
	a.wg.Add(1)
	go a.runCompletions()
	t.Cleanup(a.wg.Wait)
	return completionReplica{a: a, session: session}
}

// sharedValkeyBus returns a constructor of one replica's bus: its own client
// on the one Valkey.
func sharedValkeyBus(t *testing.T) (func() approval.Completions, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	return func() approval.Completions {
		client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
		require.NoError(t, err)
		t.Cleanup(client.Close)
		return approval.NewValkeyCompletions(client, "muster:")
	}, srv
}

func subscribed(t *testing.T, srv *miniredis.Miniredis, n int) {
	t.Helper()
	channel := approval.CompletionChannel("muster:")
	require.Eventually(t, func() bool { return srv.PubSubNumSub(channel)[channel] >= n }, 5*time.Second, 5*time.Millisecond)
}

// A decision served by another replica reaches the asked session on the
// replica that holds it.
func TestApprovalCompletionReachesTheReplicaHoldingTheSession(t *testing.T) {
	newBus, srv := sharedValkeyBus(t)
	asking := newCompletionReplica(t, newBus(), "transport-asked")
	deciding := newCompletionReplica(t, newBus(), "transport-other")
	subscribed(t, srv, 2)
	asking.a.approvalElicitations.add("req-1", asking.session.id, "el-1", time.Now().Add(time.Minute))

	deciding.a.completeApprovalElicitations(&api.ApprovalRequest{ID: "req-1", ElicitationID: "el-1"})

	notification := receive(t, asking.session)
	require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
	require.Equal(t, map[string]any{"elicitationId": "el-1"}, notification.Params.AdditionalFields)
	require.Empty(t, deciding.session.ch, "the deciding replica's own session was not asked")
	require.Empty(t, asking.a.approvalElicitations.take("req-1"), "delivered once, forgotten")
}

// The deciding replica delivers to its own sessions at once and gets its own
// notice back from the bus: the session is told once, not twice.
func TestApprovalCompletionOnTheSameReplicaIsToldOnce(t *testing.T) {
	newBus, srv := sharedValkeyBus(t)
	r := newCompletionReplica(t, newBus(), "transport-asked")
	subscribed(t, srv, 1)
	r.a.approvalElicitations.add("req-1", r.session.id, "el-1", time.Now().Add(time.Minute))

	r.a.completeApprovalElicitations(&api.ApprovalRequest{ID: "req-1", ElicitationID: "el-1"})
	require.Len(t, r.session.ch, 1)
	<-r.session.ch

	// A duplicate notice (also a replay from the bus) finds nothing to send.
	r.a.receiveCompletion(approval.Completion{Kind: approval.CompletionApproval, Key: "req-1"})
	require.Empty(t, r.session.ch)
}

func TestSignInCompletionReachesTheReplicaHoldingTheSession(t *testing.T) {
	newBus, srv := sharedValkeyBus(t)
	asking := newCompletionReplica(t, newBus(), "transport-asked")
	completing := newCompletionReplica(t, newBus(), "transport-other")
	subscribed(t, srv, 2)
	elicitationID := asking.a.signInElicitations.add("family-1", "github", asking.session.id, testAuthURL)

	completing.a.completeSignInElicitations("family-1", "github")

	notification := receive(t, asking.session)
	require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
	require.Equal(t, map[string]any{"elicitationId": elicitationID}, notification.Params.AdditionalFields)
	require.Empty(t, completing.session.ch)
	require.Empty(t, asking.a.signInElicitations.take("family-1", "github"))
}

// The in-process bus (no Valkey) behaves the same for replicas in one process.
func TestCompletionOnTheMemoryBus(t *testing.T) {
	bus := approval.NewMemoryCompletions()
	asking := newCompletionReplica(t, bus, "transport-asked")
	deciding := newCompletionReplica(t, bus, "transport-other")
	require.Eventually(t, func() bool { return bus.Subscribers() == 2 }, 5*time.Second, 5*time.Millisecond)
	asking.a.approvalElicitations.add("req-1", asking.session.id, "el-1", time.Now().Add(time.Minute))

	deciding.a.completeApprovalElicitations(&api.ApprovalRequest{ID: "req-1", ElicitationID: "el-1"})

	notification := receive(t, asking.session)
	require.Equal(t, "el-1", notification.Params.AdditionalFields["elicitationId"])
}

// receive waits for the notification the session is sent.
func receive(t *testing.T, s *legacySession) mcp.JSONRPCNotification {
	t.Helper()
	select {
	case n := <-s.ch:
		return n
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no notification reached the session")
	}
	return mcp.JSONRPCNotification{}
}

type failingBus struct{ approval.Completions }

func (failingBus) Publish(context.Context, approval.Completion) error {
	return errors.New("valkey down")
}

// An unavailable store costs the other replicas' sessions their notice, never
// the decider an error, and the local session is still told.
func TestCompletionFallsBackWhenTheStoreIsUnavailable(t *testing.T) {
	r := newCompletionReplica(t, failingBus{approval.NewMemoryCompletions()}, "transport-asked")
	r.a.approvalElicitations.add("req-1", r.session.id, "el-1", time.Now().Add(time.Minute))

	require.NotPanics(t, func() {
		r.a.completeApprovalElicitations(&api.ApprovalRequest{ID: "req-1", ElicitationID: "el-1"})
	})
	require.Len(t, r.session.ch, 1, "this replica's session is told without the store")
}

func TestCompletionNoticeCarriesNoPersonData(t *testing.T) {
	c := approval.Completion{Kind: approval.CompletionApproval, Key: "req-1", ElicitationID: "el-1"}
	require.Equal(t, approval.Completion{Kind: "approval", Key: "req-1", ElicitationID: "el-1"}, c)
	require.NotContains(t, approval.SignInKey("family-1", "github"), "family-1")
}
