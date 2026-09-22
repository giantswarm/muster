package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// TestChallengeRecorder_keepsTheBearerChallengeOfTheLast401 pins what the
// recorder keeps: the Bearer challenge of a 401, never a non-401 response, and
// the parameters a backend states about the rejected credential.
func TestChallengeRecorder_keepsTheBearerChallengeOfTheLast401(t *testing.T) {
	rec := &challengeRecorder{}
	assert.Nil(t, rec.challenge(), "nothing recorded yet")

	ok := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
	ok.Header.Add(pkgoauth.HeaderWWWAuth, `Bearer error="ignored"`)
	rec.record(ok)
	assert.Nil(t, rec.challenge(), "a 200 is not a challenge, whatever headers it carries")

	rejected := &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}}
	rejected.Header.Add(pkgoauth.HeaderWWWAuth, `Basic realm="legacy"`)
	rejected.Header.Add(pkgoauth.HeaderWWWAuth, `Bearer error="invalid_token", error_description="no identity token to act with towards the Kubernetes API"`)
	rec.record(rejected)

	challenge := rec.challenge()
	require.NotNil(t, challenge)
	assert.Equal(t, "Bearer", challenge.Scheme, "the Bearer challenge wins over other schemes")
	assert.Equal(t, "invalid_token", challenge.Error)
	assert.Equal(t, "no identity token to act with towards the Kubernetes API", challenge.ErrorDescription)

	bare := &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}}
	rec.record(bare)
	require.NotNil(t, rec.challenge(), "a 401 without a header keeps the previous challenge")

	var nilRec *challengeRecorder
	nilRec.record(rejected)
	assert.Nil(t, nilRec.challenge(), "nil recorder is a no-op")
}

// TestChallengeRecorder_keepsTheStatusOfTheLastPOST: the status the endpoint
// answered the initialize with survives to InitializeRefusedError; the
// listener's GET, which interleaves with it, never overwrites it.
func TestChallengeRecorder_keepsTheStatusOfTheLastPOST(t *testing.T) {
	rec := &challengeRecorder{}
	assert.Equal(t, 0, rec.lastPOSTStatus(), "nothing recorded yet")

	notFound := &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Request: &http.Request{Method: http.MethodPost}}
	rec.record(notFound)
	assert.Equal(t, http.StatusNotFound, rec.lastPOSTStatus())

	streamRefused := &http.Response{StatusCode: http.StatusMethodNotAllowed, Header: http.Header{}, Request: &http.Request{Method: http.MethodGet}}
	rec.record(streamRefused)
	assert.Equal(t, http.StatusNotFound, rec.lastPOSTStatus(), "a GET's answer is not the initialize's")

	noRequest := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}
	rec.record(noRequest)
	assert.Equal(t, http.StatusNotFound, rec.lastPOSTStatus(), "a response without its request is not attributed")

	var nilRec *challengeRecorder
	nilRec.record(notFound)
	assert.Equal(t, 0, nilRec.lastPOSTStatus(), "nil recorder is a no-op")
}

// TestStreamableHTTPClient_Initialize_refusedIsTypedWithTheStatus: a backend
// that answers the initialize POST with a 4xx other than 401 -- here the 404 of
// a path not served yet -- yields an InitializeRefusedError carrying the status
// mcp-go drops, still recognisable as the transport's legacy-SSE sentinel.
func TestStreamableHTTPClient_Initialize_refusedIsTypedWithTheStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}))
			defer backend.Close()

			client := NewStreamableHTTPClientWithHeaders(backend.URL+"/mcp", nil)
			err := client.Initialize(context.Background())
			require.Error(t, err)

			var refused *InitializeRefusedError
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, status, refused.StatusCode)
			assert.Equal(t, backend.URL+"/mcp", refused.URL)
			assert.ErrorIs(t, err, transport.ErrLegacySSEServer, "the transport's sentinel stays in the chain")
			assert.Contains(t, err.Error(), fmt.Sprintf("endpoint answered the initialize POST with HTTP %d", status))
			var authErr *AuthRequiredError
			assert.False(t, errors.As(err, &authErr), "a refusal is not a 401")
		})
	}
}

func TestDescribeChallenge(t *testing.T) {
	assert.Equal(t, "", DescribeChallenge(nil))
	assert.Equal(t, "", DescribeChallenge(&pkgoauth.AuthChallenge{Scheme: "Bearer", ResourceMetadataURL: "https://b/.well-known/oauth-protected-resource"}),
		"a challenge without error parameters has nothing to describe")
	assert.Equal(t, `backend WWW-Authenticate: error="invalid_token"`,
		DescribeChallenge(&pkgoauth.AuthChallenge{Error: "invalid_token"}))
	assert.Equal(t, `backend WWW-Authenticate: error="invalid_token", error_description="aud not trusted"`,
		DescribeChallenge(&pkgoauth.AuthChallenge{Error: "invalid_token", ErrorDescription: "aud not trusted"}))
	assert.Equal(t, `backend WWW-Authenticate: error_description="say \"hi\""`,
		DescribeChallenge(&pkgoauth.AuthChallenge{ErrorDescription: `say "hi"`}))
}

// TestStreamableHTTPClient_Initialize_attachesTheBackendChallenge drives the
// real client against a backend that refuses every request with a 401 whose
// WWW-Authenticate explains why, and asserts the explanation reaches the
// AuthRequiredError the caller gets — the piece mcp-go's sentinel drops.
func TestStreamableHTTPClient_Initialize_attachesTheBackendChallenge(t *testing.T) {
	var sawAuthorization string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthorization = r.Header.Get("Authorization")
		w.Header().Set(pkgoauth.HeaderWWWAuth,
			`Bearer resource_metadata="`+"http://"+r.Host+`/.well-known/oauth-protected-resource", error="invalid_token", error_description="no identity token to act with towards the Kubernetes API"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer backend.Close()

	client := NewStreamableHTTPClientWithHeaders(backend.URL, map[string]string{"Authorization": "Bearer forwarded-token"})
	err := client.Initialize(context.Background())
	require.Error(t, err)

	var authErr *AuthRequiredError
	require.ErrorAs(t, err, &authErr)
	require.NotNil(t, authErr.Challenge, "the 401's WWW-Authenticate must be attached to the error")
	assert.Equal(t, "invalid_token", authErr.Challenge.Error)
	assert.Equal(t, "no identity token to act with towards the Kubernetes API", authErr.Challenge.ErrorDescription)
	assert.Equal(t, "Bearer forwarded-token", sawAuthorization, "the configured header still reaches the backend through the recording transport")
	assert.NotContains(t, authErr.Error(), "forwarded-token", "the error never carries the credential")
	assert.Equal(t, "authentication required: server returned 401 Unauthorized: unauthorized (401)", authErr.Error(),
		"the error text stays stable; the challenge is a separate field")
}

// TestStreamableHTTPClient_Initialize_noChallengeWithoutHeader: a bare 401 is
// still an AuthRequiredError, with no challenge to report.
func TestStreamableHTTPClient_Initialize_noChallengeWithoutHeader(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer backend.Close()

	client := NewStreamableHTTPClientWithHeaders(backend.URL, nil)
	err := client.Initialize(context.Background())
	var authErr *AuthRequiredError
	require.ErrorAs(t, err, &authErr)
	assert.Nil(t, authErr.Challenge)
}

// TestRecordingHTTPClient_doesNotModifyTheCallersClient: the SigV4 and meta
// paths hand over their own client; the recorder wraps a copy.
func TestRecordingHTTPClient_doesNotModifyTheCallersClient(t *testing.T) {
	original := &http.Client{Transport: http.DefaultTransport}
	rec := &challengeRecorder{}
	wrapped := recordingHTTPClient(original, rec)
	assert.Same(t, http.DefaultTransport, original.Transport, "caller's client untouched")
	rt, ok := wrapped.Transport.(*challengeRecordingTransport)
	require.True(t, ok)
	assert.Same(t, http.DefaultTransport, rt.next)

	fromNil := recordingHTTPClient(nil, rec)
	rt, ok = fromNil.Transport.(*challengeRecordingTransport)
	require.True(t, ok)
	assert.Same(t, http.DefaultTransport, rt.next)
	assert.Zero(t, fromNil.Timeout, "no timeout: it would cut the continuous-listening GET")
}

// TestChallengeRecorder_keepsTheRetryAfterOfTheLastPOST: a Retry-After the
// endpoint sent on the initialize POST survives to lastPOSTRetryAfter, parsed
// from seconds or an HTTP-date; a GET's header never overwrites it.
func TestChallengeRecorder_keepsTheRetryAfterOfTheLastPOST(t *testing.T) {
	rec := &challengeRecorder{}
	assert.Equal(t, time.Duration(0), rec.lastPOSTRetryAfter(), "nothing recorded yet")

	rateLimited := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Request: &http.Request{Method: http.MethodPost}}
	rateLimited.Header.Set("Retry-After", "2")
	rec.record(rateLimited)
	assert.Equal(t, 2*time.Second, rec.lastPOSTRetryAfter())

	// A later POST with no Retry-After clears it: the recorder reports the
	// last POST, not the last one that carried a header.
	plain := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: &http.Request{Method: http.MethodPost}}
	rec.record(plain)
	assert.Equal(t, time.Duration(0), rec.lastPOSTRetryAfter())
}

// TestParseRetryAfter covers the two RFC 9110 forms and the values that mean
// "no wait": empty, unparsable, zero, negative, a date in the past.
func TestParseRetryAfter(t *testing.T) {
	assert.Equal(t, time.Duration(0), parseRetryAfter(""))
	assert.Equal(t, time.Duration(0), parseRetryAfter("soon"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("0"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("-5"))
	assert.Equal(t, 3*time.Second, parseRetryAfter("3"))
	assert.Equal(t, 90*time.Second, parseRetryAfter(" 90 "))
	assert.Equal(t, time.Duration(0), parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)),
		"a date already past is no wait")
	future := parseRetryAfter(time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat))
	assert.Greater(t, future, 25*time.Second, "an HTTP-date resolves to the delay until then")
	assert.LessOrEqual(t, future, 30*time.Second)
}

// TestInitializeError classifies the shapes mcp-go hands back from a failed
// initialize: a 4xx sentinel and a recorded 429 become a typed
// InitializeRefusedError carrying the status and Retry-After; a 503 in an
// error's text is typed too when the recorder saw the 503 on the POST; any
// other failure is wrapped, not typed.
func TestInitializeError(t *testing.T) {
	// A 4xx: mcp-go's sentinel, the status and Retry-After off the recorder.
	rec := &challengeRecorder{}
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Request: &http.Request{Method: http.MethodPost}}
	resp.Header.Set("Retry-After", "1")
	rec.record(resp)
	err := initializeError("http://b/mcp", transport.ErrLegacySSEServer, rec)
	var refused *InitializeRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, http.StatusTooManyRequests, refused.StatusCode)
	assert.Equal(t, time.Second, refused.RetryAfter)
	assert.ErrorIs(t, err, transport.ErrLegacySSEServer)

	// A 503: mcp-go returns a status-in-text error (not the sentinel), but the
	// recorder saw the 503 on the POST, so it is typed as retry-later too.
	rec = &challengeRecorder{}
	resp = &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Request: &http.Request{Method: http.MethodPost}}
	resp.Header.Set("Retry-After", "4")
	rec.record(resp)
	err = initializeError("http://b/mcp", fmt.Errorf("request failed with status 503: Service Unavailable"), rec)
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, http.StatusServiceUnavailable, refused.StatusCode)
	assert.Equal(t, 4*time.Second, refused.RetryAfter)

	// A 500 the recorder saw is not retry-later here: it is not the sentinel
	// and not 429/503, so it stays a plain initialize error (the service
	// classifies 5xx transient by its text as before).
	rec = &challengeRecorder{}
	rec.record(&http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{}, Request: &http.Request{Method: http.MethodPost}})
	err = initializeError("http://b/mcp", fmt.Errorf("request failed with status 500: Internal Server Error"), rec)
	var notRefused *InitializeRefusedError
	assert.False(t, errors.As(err, &notRefused), "a 500 is not typed as a refused/retry-later initialize")
	assert.Contains(t, err.Error(), "failed to initialize MCP protocol")
}

// TestDynamicAuthClient_Initialize_refusedIsTypedWithTheStatus is the
// regression for issue #1303 on the per-session OAuth client: a 429 (or any
// 4xx) to its initialize POST is typed with the status and the Retry-After,
// recognisable as the transport's legacy-SSE sentinel, and never mistaken for
// a 401. Before the fix this path returned a generic "failed to initialize MCP
// protocol" that read as a legacy SSE server.
func TestDynamicAuthClient_Initialize_refusedIsTypedWithTheStatus(t *testing.T) {
	for _, tc := range []struct {
		status     int
		retryAfter string
		wantRetry  time.Duration
	}{
		{http.StatusTooManyRequests, "1", time.Second},
		{http.StatusServiceUnavailable, "2", 2 * time.Second},
		{http.StatusNotFound, "", 0},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				http.Error(w, http.StatusText(tc.status), tc.status)
			}))
			defer backend.Close()

			client := NewDynamicAuthClient(backend.URL+"/mcp", nil, "openid", "client-id", "")
			err := client.Initialize(context.Background())
			require.Error(t, err)

			var refused *InitializeRefusedError
			require.ErrorAs(t, err, &refused, "the per-session client types the refusal, like the static-header client")
			assert.Equal(t, tc.status, refused.StatusCode, "the status mcp-go drops reaches the caller")
			assert.Equal(t, tc.wantRetry, refused.RetryAfter)

			var authErr *AuthRequiredError
			assert.False(t, errors.As(err, &authErr), "a refusal is not a 401")
			if tc.status == http.StatusNotFound {
				assert.NotContains(t, err.Error(), "legacy SSE server", "only a 405 blames legacy SSE")
			}
		})
	}
}
