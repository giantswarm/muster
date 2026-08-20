package api

// OutboundProtocolVersion is the MCP revision muster negotiates with downstream
// servers. It is deliberately not mcp.LATEST_PROTOCOL_VERSION: that constant
// moves with the SDK, and on mcp-go v1 it becomes 2026-07-28, which the
// initialize handshake cannot negotiate. Changing eras is a reviewed change,
// not a dependency bump.
const OutboundProtocolVersion = "2025-11-25"

// ServiceDataProtocolVersion is the GetServiceData key under which an MCP
// server service reports the revision its backend answered with. Both
// core_service_status (via ServiceStatus.Metadata) and core_mcpserver_get
// (via MCPServerInfo.ProtocolVersion) read it from here.
const ServiceDataProtocolVersion = "protocolVersion"
