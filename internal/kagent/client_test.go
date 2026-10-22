package kagent

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	apiv1alpha1 "github.com/giantswarm/muster/v5/internal/kagent/gen/kagent/api/v1alpha1"
)

// ownerScopedSessions answers GetSession as the kagent controller does: only
// to the bearer of the Session's owner, NotFound to any other.
type ownerScopedSessions struct {
	apiv1alpha1.UnimplementedSessionServiceServer
	owners map[string]string // session id -> owner's bearer
	err    error
}

func (s *ownerScopedSessions) GetSession(ctx context.Context, req *apiv1alpha1.GetSessionRequest) (*apiv1alpha1.GetSessionResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	md, _ := metadata.FromIncomingContext(ctx)
	auth := md.Get("authorization")
	if len(auth) != 1 {
		return nil, status.Error(codes.Unauthenticated, "no bearer")
	}
	if owner, ok := s.owners[req.GetSessionId()]; !ok || auth[0] != "Bearer "+owner {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	return &apiv1alpha1.GetSessionResponse{Session: &apiv1alpha1.Session{
		Id:           req.GetSessionId(),
		Agent:        &apiv1alpha1.ResourceReference{Namespace: "kagent", Name: "swarm-operator"},
		A2AAuthority: "session-" + req.GetSessionId() + ".kagent.actors.resources.substrate.ate.dev",
	}}, nil
}

func newTestClient(t *testing.T, srv apiv1alpha1.SessionServiceServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	apiv1alpha1.RegisterSessionServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewClient(conn)
}

func TestGetSessionAsTheCaller(t *testing.T) {
	c := newTestClient(t, &ownerScopedSessions{owners: map[string]string{"s1": "alice-token"}})

	s, err := c.GetSession(t.Context(), "alice-token", "s1")
	require.NoError(t, err)
	assert.Equal(t, &Session{ID: "s1", AgentNamespace: "kagent", A2AAuthority: "session-s1.kagent.actors.resources.substrate.ate.dev"}, s)

	_, err = c.GetSession(t.Context(), "bob-token", "s1")
	assert.ErrorIs(t, err, ErrSessionNotFound, "another person's Session")
	_, err = c.GetSession(t.Context(), "alice-token", "s2")
	assert.ErrorIs(t, err, ErrSessionNotFound, "an unknown Session")
	_, err = c.GetSession(t.Context(), "", "s1")
	assert.ErrorIs(t, err, ErrNoToken, "never asked without the caller's bearer")
}

func TestGetSessionMapsTheControllersRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"unauthenticated":   {status.Error(codes.Unauthenticated, "bad token"), ErrUnauthenticated},
		"permission denied": {status.Error(codes.PermissionDenied, "denied"), ErrSessionNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newTestClient(t, &ownerScopedSessions{err: tc.err}).GetSession(t.Context(), "t", "s1")
			assert.ErrorIs(t, err, tc.want)
		})
	}
	_, err := newTestClient(t, &ownerScopedSessions{err: status.Error(codes.Unavailable, "down")}).GetSession(t.Context(), "t", "s1")
	require.Error(t, err)
	for _, sentinel := range []error{ErrSessionNotFound, ErrUnauthenticated, ErrNoToken} {
		assert.False(t, errors.Is(err, sentinel), "an outage is no refusal")
	}
}

func TestParseTarget(t *testing.T) {
	for target, want := range map[string]struct {
		hostPort string
		tls      bool
	}{
		"grpc://agentgateway.agent-platform.svc:8080": {"agentgateway.agent-platform.svc:8080", false},
		"grpcs://kagent.example.com":                  {"kagent.example.com:443", true},
		"grpcs://kagent.example.com:8443/":            {"kagent.example.com:8443", true},
	} {
		hostPort, useTLS, err := ParseTarget(target)
		require.NoError(t, err, target)
		assert.Equal(t, want.hostPort, hostPort, target)
		assert.Equal(t, want.tls, useTLS, target)
	}
	for _, bad := range []string{"", "http://kagent:8080", "grpc://kagent", "grpc://kagent:8080/path", "grpc://u@kagent:8080", "grpc://kagent:8080?x=1"} {
		_, _, err := ParseTarget(bad)
		assert.Error(t, err, bad)
	}
}
