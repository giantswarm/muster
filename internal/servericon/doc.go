// Package servericon keeps the icon each MCP server declares in its
// initialize result, as a data: URI the MCPServer status carries
// (status.icon). It picks one of the server's icons, validates a data: URI
// or fetches an https one behind an SSRF guard, and tells the reconciler
// when a server's icon changed.
package servericon
