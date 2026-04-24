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
	ToolNames   []string // Names of available tools
	RsrcCount   int
	PromptCount int
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
