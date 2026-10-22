package kagent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	apiv1alpha1 "github.com/giantswarm/muster/v5/internal/kagent/gen/kagent/api/v1alpha1"
)

// Target schemes: plaintext h2c (an in-cluster Service) and TLS.
const (
	SchemePlaintext = "grpc"
	SchemeTLS       = "grpcs"
)

var (
	// ErrNoToken is a call without a caller bearer: there is no other
	// identity to make it with.
	ErrNoToken = errors.New("no caller token")
	// ErrSessionNotFound is a Session the controller does not answer to the
	// caller: missing, deleted, or another person's.
	ErrSessionNotFound = errors.New("session not found")
	// ErrUnauthenticated is a caller bearer the controller refused.
	ErrUnauthenticated = errors.New("the kagent controller did not accept the caller's token")
)

// Session is what muster reads of a kagent Session.
type Session struct {
	// ID is the Session's id.
	ID string
	// AgentNamespace is the namespace of the Session's Agent.
	AgentNamespace string
	// A2AAuthority is the Session's actor address:
	// <actor>.<atespace>.actors.resources.substrate.ate.dev.
	A2AAuthority string
}

// Client speaks to one kagent controller. It is safe for concurrent use.
type Client struct {
	sessions  apiv1alpha1.SessionServiceClient
	closeConn func() error
}

// ParseTarget splits a grpc:// or grpcs:// target into the host:port the gRPC
// client dials and whether the connection uses TLS. A grpcs target without a
// port is 443; a grpc target must name its port.
func ParseTarget(target string) (hostPort string, useTLS bool, err error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", false, fmt.Errorf("kagent target %q: %w", target, err)
	}
	switch u.Scheme {
	case SchemePlaintext:
	case SchemeTLS:
		useTLS = true
	default:
		return "", false, fmt.Errorf("kagent target %q must use the %s:// or %s:// scheme", target, SchemePlaintext, SchemeTLS)
	}
	if u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return "", false, fmt.Errorf("kagent target %q must be %s://host[:port] with no path", target, u.Scheme)
	}
	if u.Port() != "" {
		return u.Host, useTLS, nil
	}
	if !useTLS {
		return "", false, fmt.Errorf("kagent target %q must name a port", target)
	}
	return net.JoinHostPort(u.Hostname(), "443"), true, nil
}

// Dial builds a Client for target. The connection is established lazily on
// the first call.
func Dial(target string) (*Client, error) {
	hostPort, useTLS, err := ParseTarget(target)
	if err != nil {
		return nil, err
	}
	creds := insecure.NewCredentials()
	if useTLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient(hostPort, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("kagent: dial %s: %w", target, err)
	}
	c := NewClient(conn)
	c.closeConn = conn.Close
	return c, nil
}

// NewClient builds a Client on an existing connection.
func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{
		sessions:  apiv1alpha1.NewSessionServiceClient(conn),
		closeConn: func() error { return nil },
	}
}

// Close releases the connection Dial opened.
func (c *Client) Close() error { return c.closeConn() }

// GetSession is SessionService/GetSession as the caller: bearer is the
// call's only identity.
func (c *Client) GetSession(ctx context.Context, bearer, sessionID string) (*Session, error) {
	if bearer == "" {
		return nil, ErrNoToken
	}
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	resp, err := c.sessions.GetSession(ctx, &apiv1alpha1.GetSessionRequest{SessionId: sessionID})
	switch status.Code(err) {
	case codes.OK:
	case codes.NotFound, codes.PermissionDenied:
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	case codes.Unauthenticated:
		return nil, ErrUnauthenticated
	default:
		return nil, fmt.Errorf("kagent GetSession %s: %w", sessionID, err)
	}
	s := resp.GetSession()
	return &Session{
		ID:             s.GetId(),
		AgentNamespace: s.GetAgent().GetNamespace(),
		A2AAuthority:   s.GetA2AAuthority(),
	}, nil
}
