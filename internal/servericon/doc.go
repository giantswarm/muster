// Package servericon keeps the icon each MCP server declares in its
// initialize result, as a data: URI the MCPServer status carries
// (status.icon). It picks one of the server's icons, validates a data: URI
// or fetches an https one behind an SSRF guard, sanitises SVG with an
// allowlist, and tells the reconciler when a server's icon changed. It also
// holds the built-in brand icons used when a server has none of its own.
package servericon
