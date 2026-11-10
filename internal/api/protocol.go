package api

// ClientProtocolVersion is the MCP revision the agent's client to a muster
// instance, the BDD test client and the transport detection probe ask for.
// The aggregator's clients to downstream servers ask for 2026-07-28 and fall
// back per server (internal/mcpserver client_protocol.go).
//
// It is deliberately not mcp.LATEST_PROTOCOL_VERSION: that constant moves with
// the SDK, and on mcp-go v1 it becomes 2026-07-28, which the initialize
// handshake cannot negotiate. Changing eras is a reviewed change, not a
// dependency bump.
const ClientProtocolVersion = "2025-11-25"

// ServiceDataProtocolVersion is the GetServiceData key under which an MCP
// server service reports the revision its backend answered with. Both
// core_service_status (via ServiceStatus.Metadata) and core_mcpserver_get
// (via MCPServerInfo.ProtocolVersion) read it from here.
const ServiceDataProtocolVersion = "protocolVersion"
