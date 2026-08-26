package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awsTimeFormat is the X-Amz-Date wire format. Tests parse it back out of a
// signed request so a replayed signature is computed at the same instant.
const awsTimeFormat = "20060102T150405Z"

const (
	testSigV4Region  = "eu-central-1"
	testSigV4Service = "aws-mcp"
	testSigV4URL     = "https://aws-mcp.eu-central-1.api.aws/mcp"
)

// capturingRoundTripper records the request the transport under test forwards.
type capturingRoundTripper struct {
	req  *http.Request
	body []byte
}

func (c *capturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.req = req
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		c.body = body
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func testCredentials() aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")
}

// newTestTransport returns a signing transport and the recorder behind it.
func newTestTransport() (*sigV4Transport, *capturingRoundTripper) {
	base := &capturingRoundTripper{}
	return &sigV4Transport{
		base:    base,
		signer:  v4.NewSigner(),
		creds:   testCredentials(),
		region:  testSigV4Region,
		service: testSigV4Service,
	}, base
}

// newTestSigningChain returns the transport chain a SigV4 server actually runs:
// metadata injection in front of signing, exactly as newSigV4Client builds it.
func newTestSigningChain(meta map[string]string) (http.RoundTripper, *capturingRoundTripper) {
	signing, base := newTestTransport()
	return newMetaTransport(signing, meta), base
}

// signedHeadersOf extracts the SignedHeaders list from an Authorization header.
func signedHeadersOf(t *testing.T, authorization string) []string {
	t.Helper()
	for _, part := range strings.Split(authorization, ", ") {
		if value, found := strings.CutPrefix(part, "SignedHeaders="); found {
			return strings.Split(value, ";")
		}
	}
	t.Fatalf("no SignedHeaders in %q", authorization)
	return nil
}

// replaySignature re-signs the captured request over the given payload at the
// instant the transport signed it, and returns the Authorization header.
func replaySignature(t *testing.T, captured *http.Request, payload []byte) string {
	t.Helper()

	signedAt, err := time.Parse(awsTimeFormat, captured.Header.Get("X-Amz-Date"))
	require.NoError(t, err, "signed request carries no parsable X-Amz-Date")

	replay := captured.Clone(context.Background())
	replay.Header.Del("Authorization")
	replay.Body = io.NopCloser(bytes.NewReader(payload))
	replay.ContentLength = int64(len(payload))

	hash := sha256.Sum256(payload)
	require.NoError(t, v4.NewSigner().SignHTTP(
		context.Background(), aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"},
		replay, hex.EncodeToString(hash[:]), testSigV4Service, testSigV4Region, signedAt,
	))
	return replay.Header.Get("Authorization")
}

func TestSigV4TransportSignsTheRewrittenBody(t *testing.T) {
	original := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"aws___call_aws"}}`
	chain, base := newTestSigningChain(map[string]string{"AWS_REGION": "us-east-1"})

	req, err := http.NewRequest(http.MethodPost, testSigV4URL, strings.NewReader(original))
	require.NoError(t, err)

	resp, err := chain.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The metadata reached the wire, and the JSON-RPC id survived as an integer.
	assert.JSONEq(t,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"aws___call_aws","_meta":{"AWS_REGION":"us-east-1"}}}`,
		string(base.body))

	// The signature covers the body the server received, not the one the caller
	// handed in. Both halves matter: the first proves the order is rewrite-then-sign,
	// the second proves the test would notice the opposite order.
	authorization := base.req.Header.Get("Authorization")
	assert.Equal(t, replaySignature(t, base.req, base.body), authorization)
	assert.NotEqual(t, replaySignature(t, base.req, []byte(original)), authorization)
}

func TestSigV4TransportDoesNotSignTheConnectionHeader(t *testing.T) {
	transport, base := newTestTransport()

	req, err := http.NewRequest(http.MethodPost, testSigV4URL, strings.NewReader(`{"jsonrpc":"2.0"}`))
	require.NoError(t, err)
	req.Header.Set("Connection", "keep-alive")

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	assert.Empty(t, base.req.Header.Get("Connection"),
		"a hop-by-hop header net/http rewrites must not travel with the signature")
	assert.NotContains(t, signedHeadersOf(t, base.req.Header.Get("Authorization")), "connection")
}

func TestSigV4TransportSignsContentLengthForBodiedRequests(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	transport, base := newTestTransport()

	req, err := http.NewRequest(http.MethodPost, testSigV4URL, strings.NewReader(body))
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	// Content-Length is signed from req.ContentLength, not from the header map.
	assert.Equal(t, int64(len(body)), base.req.ContentLength)
	assert.Contains(t, signedHeadersOf(t, base.req.Header.Get("Authorization")), "content-length")
}

// TestSigV4TransportSendsTheContentHashHeader covers the header v4.SignHTTP
// does not add. Without it an S3-style backend rejects the request with a
// signature error, and auth.sigv4.service is user-settable, so muster cannot
// assume the endpoint is one of the services that ignore it.
func TestSigV4TransportSendsTheContentHashHeader(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "with a body", body: `{"jsonrpc":"2.0","id":1,"method":"ping"}`},
		{name: "without a body", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, base := newTestTransport()

			var req *http.Request
			var err error
			if tt.body == "" {
				req, err = http.NewRequest(http.MethodGet, testSigV4URL, nil)
			} else {
				req, err = http.NewRequest(http.MethodPost, testSigV4URL, strings.NewReader(tt.body))
			}
			require.NoError(t, err)

			_, err = transport.RoundTrip(req)
			require.NoError(t, err)

			// The value is the hash the signature was computed over, so an
			// endpoint that verifies the header against the payload agrees.
			want := sha256.Sum256([]byte(tt.body))
			assert.Equal(t, hex.EncodeToString(want[:]), base.req.Header.Get(contentSHA256Header))

			// Set before signing, so it travels inside SignedHeaders. A header
			// added afterwards would be ignored by the endpoint at best.
			assert.Contains(t, signedHeadersOf(t, base.req.Header.Get("Authorization")),
				strings.ToLower(contentSHA256Header))
		})
	}
}

func TestSigV4TransportSignsBodylessRequests(t *testing.T) {
	transport, base := newTestTransport()

	// The continuous-listening GET and the session-closing DELETE carry no body.
	req, err := http.NewRequest(http.MethodGet, testSigV4URL, nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	assert.Empty(t, base.body)
	assert.NotContains(t, signedHeadersOf(t, base.req.Header.Get("Authorization")), "content-length")
	assert.Equal(t, replaySignature(t, base.req, nil), base.req.Header.Get("Authorization"))
}

func TestSigV4TransportDoesNotModifyTheOriginalRequest(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`
	chain, _ := newTestSigningChain(map[string]string{"AWS_REGION": "us-east-1"})

	req, err := http.NewRequest(http.MethodPost, testSigV4URL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Connection", "keep-alive")

	_, err = chain.RoundTrip(req)
	require.NoError(t, err)

	assert.Empty(t, req.Header.Get("Authorization"), "the signature belongs to the clone")
	assert.Empty(t, req.Header.Get(contentSHA256Header), "the payload hash belongs to the clone")
	assert.Equal(t, "keep-alive", req.Header.Get("Connection"))

	// Read through GetBody, not req.Body: RoundTrip closes req.Body as the
	// contract requires, so the caller's replay handle is what stays usable.
	replay, err := req.GetBody()
	require.NoError(t, err)
	remaining, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, body, string(remaining))
}

// closeTrackingBody reports whether RoundTrip closed the request body.
type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestSigV4TransportClosesTheRequestBody(t *testing.T) {
	tests := []struct {
		name  string
		creds aws.CredentialsProvider
	}{
		{name: "on the happy path", creds: testCredentials()},
		{name: "on the error path", creds: &countingCredentials{succeedAfter: neverSucceeds}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, _ := newTestTransport()
			transport.creds = tt.creds

			tracked := &closeTrackingBody{Reader: strings.NewReader(`{"jsonrpc":"2.0"}`)}
			req, err := http.NewRequest(http.MethodPost, testSigV4URL, tracked)
			require.NoError(t, err)
			// http.NewRequest only sets GetBody for known reader types, so this
			// also covers the fallback branch of readRequestBody.
			require.Nil(t, req.GetBody)

			_, _ = transport.RoundTrip(req)
			assert.True(t, tracked.closed, "RoundTrip must close the request body on every path")
		})
	}
}

func TestSigV4TransportReportsCredentialFailures(t *testing.T) {
	transport, base := newTestTransport()
	transport.creds = &countingCredentials{succeedAfter: neverSucceeds}

	req, err := http.NewRequest(http.MethodPost, testSigV4URL, strings.NewReader(`{}`))
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.ErrorContains(t, err, credentialFailureMessage)
	assert.Nil(t, base.req, "an unsigned request must not reach the endpoint")

	// The sentinel is what lets the service layer schedule a retry instead of
	// leaving the server failed until the pod restarts.
	require.ErrorIs(t, err, ErrAWSCredentialsUnavailable)
}
