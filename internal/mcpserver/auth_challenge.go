package mcpserver

import (
	"net/http"
	"strings"
	"sync"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// challengeRecorder keeps the WWW-Authenticate header of the most recent 401 a
// client received, so a connect failure can report what the backend said about
// the credential it rejected.
//
// mcp-go reduces a 401 to a sentinel error plus the RFC 9728 resource_metadata
// pointer; the challenge's error and error_description parameters never leave
// the transport. For a token-forwarding backend those parameters are the only
// statement of *why* the forwarded token was refused (an untrusted audience
// reads the same as an untrusted issuer from the status code alone), so the
// recorder captures the header on the wire and CheckForAuthRequiredError's
// callers attach it to the AuthRequiredError they return.
//
// The recorder also keeps the status the endpoint answered the most recent
// POST with. mcp-go reduces a 4xx to the initialize POST to
// transport.ErrLegacySSEServer without the code, and the code is what tells a
// path not served yet (404, a backend mid-rollout) from a server that speaks
// only legacy SSE (405); InitializeRefusedError carries it to the CR status.
//
// Only the header value and the status are kept: never the request, its
// Authorization header, or the response body.
type challengeRecorder struct {
	mu         sync.Mutex
	last       string
	postStatus int
}

// record keeps the status of resp when it answers a POST -- the initialize,
// never the listener's GET, which interleaves with it and answers 405 from a
// server that offers no stream -- and the Bearer challenge when it is a 401.
// Any other response leaves the challenge unchanged, so the value a connect
// failure reads is the rejection that preceded it.
func (r *challengeRecorder) record(resp *http.Response) {
	if r == nil || resp == nil {
		return
	}
	if resp.Request != nil && resp.Request.Method == http.MethodPost {
		r.mu.Lock()
		r.postStatus = resp.StatusCode
		r.mu.Unlock()
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return
	}
	values := resp.Header.Values(pkgoauth.HeaderWWWAuth)
	if len(values) == 0 {
		return
	}
	// A backend may send several challenges; the Bearer one carries the OAuth
	// parameters. Fall back to the first when none is Bearer.
	chosen := values[0]
	for _, v := range values {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "bearer") {
			chosen = v
			break
		}
	}
	r.mu.Lock()
	r.last = chosen
	r.mu.Unlock()
}

// lastPOSTStatus returns the HTTP status the endpoint answered the most
// recent POST with, or 0 when none was recorded.
func (r *challengeRecorder) lastPOSTStatus() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.postStatus
}

// challenge returns the parsed challenge of the last recorded 401, or nil when
// none was recorded or the header did not parse.
func (r *challengeRecorder) challenge() *pkgoauth.AuthChallenge {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	last := r.last
	r.mu.Unlock()
	if last == "" {
		return nil
	}
	challenge, err := pkgoauth.ParseWWWAuthenticate(last)
	if err != nil {
		return nil
	}
	return challenge
}

// challengeRecordingTransport hands every response to the recorder before
// returning it. Transport errors carry no challenge and pass through untouched.
type challengeRecordingTransport struct {
	next http.RoundTripper
	rec  *challengeRecorder
}

func (t *challengeRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err == nil {
		if resp.Request == nil {
			// net/http's Transport sets it; a custom RoundTripper may not.
			resp.Request = req
		}
		t.rec.record(resp)
	}
	return resp, err
}

// recordingHTTPClient returns a copy of client whose transport records 401
// challenges and POST statuses into rec. A nil client yields one equivalent to mcp-go's default
// (no timeout, the default transport): a timeout would also cut the
// long-lived GET that WithContinuousListening opens. The caller's client is
// not modified.
func recordingHTTPClient(client *http.Client, rec *challengeRecorder) *http.Client {
	var out http.Client
	if client != nil {
		out = *client
	}
	base := out.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out.Transport = &challengeRecordingTransport{next: base, rec: rec}
	return &out
}

// DescribeChallenge renders the parts of a backend's Bearer challenge that
// explain a rejection: the error code and error_description parameters. It
// returns "" when the challenge carries neither, so callers can append it to a
// diagnostic only when there is something to say.
func DescribeChallenge(challenge *pkgoauth.AuthChallenge) string {
	if challenge == nil || (challenge.Error == "" && challenge.ErrorDescription == "") {
		return ""
	}
	var parts []string
	if challenge.Error != "" {
		parts = append(parts, "error="+strconvQuote(challenge.Error))
	}
	if challenge.ErrorDescription != "" {
		parts = append(parts, "error_description="+strconvQuote(challenge.ErrorDescription))
	}
	return "backend WWW-Authenticate: " + strings.Join(parts, ", ")
}

// strconvQuote wraps v in double quotes, escaping any it contains, so the
// rendered parameter reads the way the backend sent it.
func strconvQuote(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}
