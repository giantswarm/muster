// Package main implements musterstdio, a standalone shim that wraps an
// stdio MCP server child process and exposes it over MCP-over-StreamableHTTP.
// It is the shim image referenced by the translator stack for stdio
// MCPServer specs: filesystem mode runs it as a local subprocess; cluster
// mode runs it as a Deployment.
package main
