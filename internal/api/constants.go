package api

// ClientSessionIDHeader is the HTTP header name for client-provided session IDs.
// This enables CLI tools to maintain persistent session identity across invocations.
//
// When present, this header takes precedence over the random session ID generated
// by mcp-go for token storage. This is critical for CLI tools where each invocation
// creates a new connection - without it, MCP server tokens would be lost between
// CLI invocations because the mcp-go session ID changes on each connection.
//
// Security: The client-provided session ID is trusted because:
//   - It's sent by the authenticated CLI client (aggregator auth validates the user)
//   - Token lookup still requires matching (sessionID, issuer, scope)
//   - A malicious client can only access tokens it previously stored with that session ID
const ClientSessionIDHeader = "X-Muster-Session-ID"
