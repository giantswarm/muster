package admin

import (
	"encoding/json"
	"time"
)

// SessionSummary is one row in the session list view.
type SessionSummary struct {
	SessionID   string
	Subject     string
	Email       string // User email from ID token (preferred over subject for display)
	ServerCount int
	ToolCount   int
	LastSeen    time.Time // Zero if unknown.
}

// SessionDetail is the full view for one session.
type SessionDetail struct {
	SessionID string
	Subject   string
	Email     string // User email from ID token (preferred over subject for display)
	Servers   []ServerEntry
	Tokens    []SessionToken // Raw JWTs to be decoded; never rendered raw.
}

// ServerEntry describes one authenticated server for a session.
type ServerEntry struct {
	Name        string
	Issuer      string
	Transport   string // "sse", "stdio", "streamable-http", or "" if not pooled.
	Pooled      bool
	CreatedAt   time.Time
	LastUsedAt  time.Time
	TokenExpiry time.Time // Zero if no tracked expiry.
	ToolCount   int
	ToolNames   []string // Sorted names of the tools advertised to this session.
	RsrcCount   int
	PromptCount int
}

// MCPSummary is one row in the global MCP server list.
type MCPSummary struct {
	Name         string
	URL          string
	Namespace    string
	Status       string // connected / disconnected / unknown (api.ServiceState string)
	Issuer       string // Empty when server does not require auth.
	RequiresAuth bool
	ToolCount    int
	RsrcCount    int
	PromptCount  int
	LastUpdate   time.Time
}

// MCPDetail is the full view for one MCP server.
type MCPDetail struct {
	MCPSummary

	ToolPrefix string
	Scope      string

	Tools     []MCPTool
	Resources []MCPResource
	Prompts   []MCPPrompt
}

// MCPTool is the rendered view for a single advertised tool.
type MCPTool struct {
	Name        string
	Description string
}

// MCPResource is the rendered view for a single advertised resource.
type MCPResource struct {
	URI         string
	Name        string
	Description string
}

// MCPPrompt is the rendered view for a single advertised prompt.
type MCPPrompt struct {
	Name        string
	Description string
}

// SessionToken pairs a raw JWT with a display label. The admin package
// decodes the payload for rendering; the raw value never leaves the server.
type SessionToken struct {
	Label string // e.g. "muster → github"
	Raw   string // Compact JWT. Never rendered to the client.
}

// DecodedJWT is the header+payload view of a JWT. The signature segment is
// always discarded before a DecodedJWT is constructed.
type DecodedJWT struct {
	Label   string
	Header  json.RawMessage
	Payload json.RawMessage
	Error   string // Non-empty if decoding failed; fields above may be nil.
}
