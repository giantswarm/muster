package mcpserver

import (
	"io"
	"net/http"
)

// readRequestBody returns the request body as bytes, or nil for a request
// without one. It prefers GetBody, which net/http populates for every body
// created from a bytes.Reader, so the caller's copy stays readable.
//
// Reading req.Body is the fallback when the caller supplied no GetBody, and it
// does consume the original — an outer retry of the same *http.Request would
// then send an empty body. mcp-go always builds bodies from a bytes.Reader, so
// GetBody is always set and that path is not taken in practice.
//
// Shared by the two round trippers that have to see the whole payload:
// metaTransport rewrites it, sigV4Transport hashes it.
func readRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}

	source := req.Body
	if req.GetBody != nil {
		replay, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = replay.Close() }()
		source = replay
	}

	// mcp-go builds every body from a bytes.Reader, so ContentLength is known
	// and one exact-size read beats letting a buffer grow into it.
	if req.ContentLength > 0 {
		body := make([]byte, req.ContentLength)
		if _, err := io.ReadFull(source, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	return io.ReadAll(source)
}
