// Package ssetransport keeps a closed server-sent-events response from holding
// an MCP call on a reused HTTP/1 connection.
//
// mcp-go's streamable HTTP client closes an SSE response body while its reader
// is still blocked in Read, and closes it a second time when the call returns.
// Go's HTTP/1 transport answers an early Close by draining the body for up to
// 50ms so it can reuse the connection. When the stream ends inside that drain,
// the blocked reader's EOF waits for a signal the connection's read loop has
// already given, holding the body's lock, and the connection goes back to the
// pool: the reader, the second Close and the next response on that connection
// wait until the idle connection is reaped (90s by default).
//
// Wrap never lets that interleaving happen. A Close while a Read is in flight
// returns at once and leaves the body to the reader: a stream that ends within
// a short grace period ends cleanly and its connection is reused; otherwise the
// request is cancelled, the transport discards the connection and the reader
// returns. Only then is the body closed, with no reader left to race the drain.
package ssetransport
