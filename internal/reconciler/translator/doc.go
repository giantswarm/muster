// Package translator turns an MCPServer spec into a backend-agnostic
// configuration model that downstream emitters (Kubernetes CRDs, agentgateway
// native YAML) serialize. Transform is pure: same input always yields the same
// Model, with no I/O. Backend is a tagged union covering streamable-http, sse,
// and stdio transports; agentgateway's `mcp.targets[].stdio` schema absorbs the
// stdio variant directly so no shim process is required in filesystem mode.
package translator
