// Package ssetest serves the frame that held an MCP call on a reused HTTP/1
// connection for the idle connection's 90s under go1.27.1's net/http (Go
// issue 81404): an SSE body closed while its reader is blocked in Read, with
// the stream ending inside the transport's post-close drain. The clients built
// on mcp-go use it to prove their calls return at once.
package ssetest
