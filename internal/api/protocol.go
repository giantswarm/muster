package api

// ClientProtocolVersion is the MCP revision the agent's client to a muster
// instance asks for when it does not answer URL elicitations itself (the stdio
// bridge, the REPL, `muster auth`), the BDD test client's default, and the
// revision the transport detection probe asks for. The aggregator's clients to downstream servers ask for
// 2026-07-28 and fall back per server (internal/mcpserver client_protocol.go).
//
// It is deliberately not mcp.LATEST_PROTOCOL_VERSION: that constant moves with
// the SDK, and on mcp-go v1 it becomes 2026-07-28, which the initialize
// handshake cannot negotiate. Changing eras is a reviewed change, not a
// dependency bump.
const ClientProtocolVersion = "2025-11-25"

// URLElicitationProtocolVersion is the MCP revision a client to a muster
// instance asks for when it answers URL mode elicitations itself: the CLI's
// commands, and the BDD test client in a scenario that sets protocol_version. On it, a tool call that needs the person
// to open a page is answered with an input_required result, retried with the
// person's answer and the echoed requestState. A server that does not speak
// it is spoken to on ClientProtocolVersion, where muster keeps its
// auth_required answer.
const URLElicitationProtocolVersion = "2026-07-28"

// ServiceDataProtocolVersion is the GetServiceData key under which an MCP
// server service reports the revision its backend answered with. Both
// core_service_status (via ServiceStatus.Metadata) and core_mcpserver_get
// (via MCPServerInfo.ProtocolVersion) read it from here.
const ServiceDataProtocolVersion = "protocolVersion"
