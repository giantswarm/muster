package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/client/transport"
)

// authLossThreshold is the number of consecutive authentication failures
// (token-store misses or HTTP 401 responses) after which a session-scoped
// OAuth client reports that its authentication is lost. It mirrors
// maxConsecutiveTokenFailures on the token-forwarding path: mcp-go's
// continuous listener retries once per second, so detection settles within a
// few seconds while a single transient 401 never tears down a connection.
const authLossThreshold = 3

// authLossDetector aggregates authentication-failure signals from the two
// places a lost OAuth grant becomes visible, and fires onAuthLost once when
// authLossThreshold consecutive failures accumulate:
//
//   - the token store: the token is gone (e.g. the backing store lost it), so
//     mcp-go fails the request before it reaches the wire
//   - the HTTP transport: a token is still held but the server rejects it
//     with 401 (e.g. it was revoked or the grant it came from is unknown)
//
// One counter serves both signals on purpose: each request produces exactly
// one of them (a store miss aborts the request, otherwise the wire result
// decides), so a mixed sequence still means consecutive failed attempts.
//
// Without this detector nothing ever stops a broken session connection:
// mcp-go's continuous-listening GET retries every second forever, logging an
// error each time, while the connection can never recover on its own — only
// a human re-authenticating mints a new grant.
type authLossDetector struct {
	// onAuthLost is invoked asynchronously, exactly once per detector, from
	// its own goroutine so it may close the very client whose request path
	// triggered it. Immutable after construction.
	onAuthLost func(reason string)

	mu          sync.Mutex
	consecutive int
	fired       bool
}

// noteFailure records one authentication failure and fires onAuthLost when
// the threshold is reached. Safe to call on a nil detector.
func (d *authLossDetector) noteFailure(reason string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.consecutive++
	fire := !d.fired && d.consecutive >= authLossThreshold && d.onAuthLost != nil
	if fire {
		d.fired = true
	}
	d.mu.Unlock()

	if fire {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logging.Error("AuthLossDetector", fmt.Errorf("panic in onAuthLost: %v", r),
						"onAuthLost callback panicked")
				}
			}()
			d.onAuthLost(reason)
		}()
	}
}

// noteSuccess resets the consecutive-failure count. Called on any HTTP
// response that is not a 401 — only the server accepting or rejecting the
// credential says anything about authentication, so a successful token-store
// read does not reset the count. Safe to call on a nil detector.
func (d *authLossDetector) noteSuccess() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.consecutive = 0
	d.mu.Unlock()
}

// authObservingTokenStore wraps a transport.TokenStore and reports token-store
// misses (transport.ErrNoToken) to the detector. mcp-go consults the store via
// GetAuthorizationHeader before every request — including the continuous
// listener's GETs — so a store that lost its token is noticed within a few
// listener iterations even when no tool call is in flight.
type authObservingTokenStore struct {
	transport.TokenStore
	detector *authLossDetector
}

func (s *authObservingTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	token, err := s.TokenStore.GetToken(ctx)
	if err != nil && errors.Is(err, transport.ErrNoToken) {
		s.detector.noteFailure("token no longer present in the token store")
	}
	return token, err
}

// authObservingTransport reports the server's verdict on the credential to the
// detector: a 401 response counts as an authentication failure, any other
// response resets the count. Transport errors say nothing about
// authentication and do neither. It covers the requests the token store
// cannot see failing — a token that is still held but no longer accepted.
type authObservingTransport struct {
	next     http.RoundTripper
	detector *authLossDetector
}

func (t *authObservingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.detector.noteFailure("server rejected the token with 401")
	} else {
		t.detector.noteSuccess()
	}
	return resp, err
}
