package api

// OutboundProtocolVersion is the MCP revision muster negotiates with downstream
// servers. It is deliberately not mcp.LATEST_PROTOCOL_VERSION: that constant
// moves with the SDK, and on mcp-go v1 it becomes 2026-07-28, which the
// initialize handshake cannot negotiate. Changing eras is a reviewed change,
// not a dependency bump.
const OutboundProtocolVersion = "2025-11-25"
