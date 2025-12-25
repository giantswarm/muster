package auth

// StatusResponse represents the structured authentication state.
// This is the canonical type returned by the auth://status resource.
type StatusResponse struct {
	// MusterAuth describes authentication to Muster Server itself
	MusterAuth *MusterAuthStatus `json:"muster_auth"`

	// ServerAuths describes authentication to each remote MCP server
	ServerAuths []ServerAuthStatus `json:"server_auths"`
}

// MusterAuthStatus describes the authentication state for Muster Server.
type MusterAuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	Issuer        string `json:"issuer,omitempty"`
}

// ServerAuthStatus describes the authentication state for a remote MCP server.
type ServerAuthStatus struct {
	// ServerName is the name of the MCP server
	ServerName string `json:"server_name"`

	// Status is one of: "connected", "auth_required", "error", "disconnected", "initializing"
	Status string `json:"status"`

	// AuthChallenge is present when Status == "auth_required"
	AuthChallenge *ChallengeInfo `json:"auth_challenge,omitempty"`

	// Error is present when Status == "error"
	Error string `json:"error,omitempty"`
}

// ChallengeInfo contains information about an authentication challenge.
type ChallengeInfo struct {
	// Issuer is the IdP URL that will issue tokens
	Issuer string `json:"issuer"`

	// Scope is the OAuth scope required
	Scope string `json:"scope,omitempty"`

	// AuthToolName is the tool to call for browser-based auth
	AuthToolName string `json:"auth_tool_name"`
}
