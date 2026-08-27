package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/giantswarm/muster/pkg/logging"
)

// metaTransport merges the configured entries into the params._meta object of
// every outbound JSON-RPC request that carries params.
//
// It is an http.RoundTripper so that every HTTP-based MCP client shares one
// implementation. It used to live inside the SigV4 signing transport, which
// made spec.meta a silent no-op on every server that was not signed. The two
// concerns are now separate, and the chain for a signed server is
// metaTransport -> sigV4Transport -> the real transport: the body has to be
// rewritten before it is signed, because SigV4 covers a hash of the payload.
type metaTransport struct {
	// base carries the rewritten request. Never nil in constructed values.
	base http.RoundTripper

	// meta is merged into params._meta of every request that carries params.
	meta map[string]string
}

// newMetaTransport wraps base in a metadata-injecting transport, and returns
// base unchanged when there is nothing to inject. A server without spec.meta
// keeps the plain transport rather than paying for a body rewrite per request.
func newMetaTransport(base http.RoundTripper, meta map[string]string) http.RoundTripper {
	if len(meta) == 0 {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &metaTransport{base: base, meta: meta}
}

// metaHTTPClient returns an HTTP client that injects meta into every outbound
// request, or nil when there is nothing to inject so the caller can leave the
// transport's own client in place.
//
// The client carries no timeout, which matches mcp-go's default. A timeout
// would also cut the long-lived GET that WithContinuousListening opens.
func metaHTTPClient(meta map[string]string) *http.Client {
	if len(meta) == 0 {
		return nil
	}
	return &http.Client{Transport: newMetaTransport(http.DefaultTransport, meta)}
}

// RoundTrip forwards a clone of req with the merged body to the base transport.
//
// req's headers and fields are not modified, and its body is read through
// req.GetBody so the caller's copy stays readable. The body is closed before
// returning, which the http.RoundTripper contract requires of every path,
// including the error paths.
func (t *metaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}

	body, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("meta: failed to read request body: %w", err)
	}

	body, err = injectMeta(body, t.meta)
	if err != nil {
		return nil, fmt.Errorf("meta: failed to inject request metadata: %w", err)
	}

	next := req.Clone(req.Context())
	if body != nil {
		next.Body = io.NopCloser(bytes.NewReader(body))
		next.ContentLength = int64(len(body))
		next.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	return t.base.RoundTrip(next)
}

// injectMeta merges meta into the params._meta object of a JSON-RPC request
// body and returns the rewritten body. A key already present in _meta wins, so
// a caller can still override per call, and a merge that adds nothing returns
// the original bytes rather than a re-marshalled equivalent.
//
// This is the only place muster reaches into an MCP message payload. It exists
// because a backend can read call-scoped configuration from the metadata field
// rather than from tool arguments: the AWS-hosted MCP server takes the region
// it operates in from params._meta.AWS_REGION and declares no region argument.
//
// Anything it does not understand is passed through unchanged and logged at
// debug level. The log is the only signal available, because a dropped entry
// fails nowhere — the backend falls back to its own region and answers
// successfully about the wrong one.
func injectMeta(body []byte, meta map[string]string) ([]byte, error) {
	// A bodyless request is normal, not a diagnostic: the continuous-listening
	// GET and the session-closing DELETE both carry no body, and neither needs
	// metadata. Return before the logging branches so they do not report it.
	if len(meta) == 0 || len(body) == 0 {
		return body, nil
	}

	// UseNumber keeps JSON-RPC ids and other integers from being re-marshalled
	// as floats, which would change the id the endpoint echoes back.
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var message map[string]any
	if err := decoder.Decode(&message); err != nil {
		// A JSON-RPC batch is a top-level array, so it lands here too. mcp-go
		// does not send batches; if that changes, each element needs the merge.
		logging.Debug("MetaTransport", "Not injecting meta: body is not a JSON object (%d bytes)", len(body))
		return body, nil
	}

	rawParams, hasParams := message["params"]
	if !hasParams {
		// ping and most notifications carry no params. Note the merge is not
		// limited to tool calls: initialize does carry params, so the handshake
		// gets the entries too. Harmless against the AWS endpoint, but it is the
		// blast radius to remember for a backend that validates them strictly.
		return body, nil
	}
	params, ok := rawParams.(map[string]any)
	if !ok {
		logging.Debug("MetaTransport", "Not injecting meta: params is %T, not an object", rawParams)
		return body, nil
	}

	var requestMeta map[string]any
	switch existing := params["_meta"].(type) {
	case nil: // absent, or an explicit JSON null: fill it in
		requestMeta = make(map[string]any, len(meta))
	case map[string]any:
		requestMeta = existing
	default:
		logging.Debug("MetaTransport", "Not injecting meta: params._meta is %T, not an object", existing)
		return body, nil
	}

	changed := false
	for key, value := range meta {
		if _, taken := requestMeta[key]; taken {
			continue
		}
		requestMeta[key] = value
		changed = true
	}
	if !changed {
		return body, nil
	}
	params["_meta"] = requestMeta

	return json.Marshal(message)
}
