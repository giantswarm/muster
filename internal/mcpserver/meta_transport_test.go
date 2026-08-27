package mcpserver

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

const testMetaURL = "https://mcp.example.com/mcp"

var testMeta = map[string]string{"AWS_REGION": "eu-central-1"}

// newTestMetaTransport returns a metadata-injecting transport and the recorder
// behind it.
func newTestMetaTransport(meta map[string]string) (http.RoundTripper, *capturingRoundTripper) {
	base := &capturingRoundTripper{}
	return newMetaTransport(base, meta), base
}

// TestMetaTransportInjectsWithoutSigning is the regression test for the finding
// that spec.meta was a silent no-op on every server that was not signed with
// SigV4. Injection now lives in its own transport, so a plain server gets it.
func TestMetaTransportInjectsWithoutSigning(t *testing.T) {
	transport, base := newTestMetaTransport(testMeta)

	req, err := http.NewRequest(http.MethodPost, testMetaURL,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}`))
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.JSONEq(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","_meta":{"AWS_REGION":"eu-central-1"}}}`,
		string(base.body))
	assert.Empty(t, base.req.Header.Get("Authorization"), "an unsigned server stays unsigned")

	// The rewritten body is longer, so a stale Content-Length would truncate it.
	assert.Equal(t, int64(len(base.body)), base.req.ContentLength)
}

// TestNewMetaTransportSkipsTheWrapperWhenThereIsNothingToInject keeps a server
// without spec.meta on the plain transport instead of paying for a body
// rewrite on every request.
func TestNewMetaTransportSkipsTheWrapperWhenThereIsNothingToInject(t *testing.T) {
	base := &capturingRoundTripper{}
	for _, meta := range []map[string]string{nil, {}} {
		assert.Same(t, base, newMetaTransport(base, meta))
	}
	assert.NotSame(t, base, newMetaTransport(base, testMeta))
}

func TestMetaHTTPClient(t *testing.T) {
	assert.Nil(t, metaHTTPClient(nil), "no entries means the transport keeps its own client")
	assert.Nil(t, metaHTTPClient(map[string]string{}))

	client := metaHTTPClient(testMeta)
	require.NotNil(t, client)
	assert.IsType(t, &metaTransport{}, client.Transport)
	assert.Zero(t, client.Timeout, "the continuous-listening GET must not be cut by a client timeout")
}

func TestMetaTransportForwardsBodylessRequests(t *testing.T) {
	transport, base := newTestMetaTransport(testMeta)

	// The continuous-listening GET and the session-closing DELETE carry no body.
	req, err := http.NewRequest(http.MethodGet, testMetaURL, nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	assert.Empty(t, base.body)
	assert.Equal(t, http.MethodGet, base.req.Method)
}

func TestMetaTransportDoesNotModifyTheOriginalRequest(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`
	transport, _ := newTestMetaTransport(testMeta)

	req, err := http.NewRequest(http.MethodPost, testMetaURL, strings.NewReader(body))
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	// Read through GetBody, not req.Body: RoundTrip closes req.Body as the
	// contract requires, so the caller's replay handle is what stays usable.
	replay, err := req.GetBody()
	require.NoError(t, err)
	remaining, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, body, string(remaining))
}

func TestMetaTransportClosesTheRequestBody(t *testing.T) {
	transport, _ := newTestMetaTransport(testMeta)

	tracked := &closeTrackingBody{Reader: strings.NewReader(`{"method":"tools/call","params":{}}`)}
	req, err := http.NewRequest(http.MethodPost, testMetaURL, tracked)
	require.NoError(t, err)
	// http.NewRequest only sets GetBody for known reader types, so this also
	// covers the fallback branch of readRequestBody.
	require.Nil(t, req.GetBody)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	assert.True(t, tracked.closed, "RoundTrip must close the request body")
}

// TestPlainRemoteClientsInjectMeta pins the wiring the finding was about: the
// factory has to hand the entries to every remote client it builds, not only
// to the signing one.
func TestPlainRemoteClientsInjectMeta(t *testing.T) {
	t.Run("streamable-http", func(t *testing.T) {
		client, err := NewMCPClientFromType(api.MCPServerTypeStreamableHTTP, MCPClientConfig{
			URL:  testMetaURL,
			Meta: testMeta,
		})
		require.NoError(t, err)

		streamable, ok := client.(*StreamableHTTPClient)
		require.True(t, ok)
		assert.Equal(t, testMeta, streamable.meta)
		assert.Nil(t, streamable.httpClientFunc, "a plain server needs no per-connect credential")
	})

	t.Run("sse", func(t *testing.T) {
		client, err := NewMCPClientFromType(api.MCPServerTypeSSE, MCPClientConfig{
			URL:  testMetaURL,
			Meta: testMeta,
		})
		require.NoError(t, err)

		sse, ok := client.(*SSEClient)
		require.True(t, ok)
		assert.Equal(t, testMeta, sse.meta)
	})

	// The session-scoped client the aggregator builds for an OAuth server takes
	// the entries too, so a server that needs a login does not lose them.
	t.Run("dynamic auth", func(t *testing.T) {
		client := NewDynamicAuthClient(testMetaURL, nil, "openid", "muster", "").WithMeta(testMeta)
		assert.Equal(t, testMeta, client.meta)
	})
}

// TestStdioRejectsMeta covers the one remote-only field that no transport can
// inject: a stdio server speaks over a pipe, so accepting the map would drop it.
func TestStdioRejectsMeta(t *testing.T) {
	_, err := NewMCPClientFromType(api.MCPServerTypeStdio, MCPClientConfig{
		Command: "echo",
		Meta:    testMeta,
	})
	require.ErrorContains(t, err, "meta is only allowed")
}

func TestInjectMeta(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]string
		body string
		want string
	}{
		{
			name: "no meta configured leaves the body alone",
			meta: nil,
			body: `{"method":"tools/call","params":{}}`,
			want: `{"method":"tools/call","params":{}}`,
		},
		{
			name: "a request without params is skipped",
			meta: testMeta,
			body: `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
			want: `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		},
		{
			name: "an empty _meta gains the entry",
			meta: testMeta,
			body: `{"method":"tools/call","params":{"_meta":{}}}`,
			want: `{"method":"tools/call","params":{"_meta":{"AWS_REGION":"eu-central-1"}}}`,
		},
		{
			name: "a value already present wins",
			meta: testMeta,
			body: `{"method":"tools/call","params":{"_meta":{"AWS_REGION":"us-east-1"}}}`,
			want: `{"method":"tools/call","params":{"_meta":{"AWS_REGION":"us-east-1"}}}`,
		},
		{
			name: "unrelated _meta keys are preserved",
			meta: testMeta,
			body: `{"method":"tools/call","params":{"_meta":{"progressToken":4}}}`,
			want: `{"method":"tools/call","params":{"_meta":{"progressToken":4,"AWS_REGION":"eu-central-1"}}}`,
		},
		{
			name: "a _meta that is not an object is left alone",
			meta: testMeta,
			body: `{"method":"tools/call","params":{"_meta":"opaque"}}`,
			want: `{"method":"tools/call","params":{"_meta":"opaque"}}`,
		},
		{
			name: "params that are not an object are left alone",
			meta: testMeta,
			body: `{"method":"tools/call","params":[1,2]}`,
			want: `{"method":"tools/call","params":[1,2]}`,
		},
		{
			name: "a null _meta counts as absent",
			meta: testMeta,
			body: `{"method":"tools/call","params":{"_meta":null}}`,
			want: `{"method":"tools/call","params":{"_meta":{"AWS_REGION":"eu-central-1"}}}`,
		},
		{
			name: "a batch is left alone",
			meta: testMeta,
			body: `[{"method":"tools/call","params":{}}]`,
			want: `[{"method":"tools/call","params":{}}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := injectMeta([]byte(tt.body), tt.meta)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestInjectMetaLeavesNonJSONBodiesAlone(t *testing.T) {
	for _, body := range []string{"", "not json at all"} {
		got, err := injectMeta([]byte(body), testMeta)
		require.NoError(t, err)
		assert.Equal(t, body, string(got))
	}
}

// TestInjectMetaReturnsTheOriginalBodyOnANoOpMerge pins the byte-for-byte
// passthrough. Re-marshalling reorders keys and changes Content-Length, so a
// merge that adds nothing must not touch the body at all.
func TestInjectMetaReturnsTheOriginalBodyOnANoOpMerge(t *testing.T) {
	body := `{"method":"tools/call","params":{"_meta":{"AWS_REGION":"us-east-1"},"name":"x"}}`
	got, err := injectMeta([]byte(body), testMeta)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}

func TestInjectMetaPreservesIntegerIDs(t *testing.T) {
	got, err := injectMeta(
		[]byte(`{"id":9007199254740993,"method":"tools/call","params":{}}`),
		testMeta,
	)
	require.NoError(t, err)
	assert.Contains(t, string(got), `"id":9007199254740993`)
}
