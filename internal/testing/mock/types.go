package mock

// ToolConfig defines configuration for a mock tool
type ToolConfig struct {
	// Name is the unique identifier for the tool
	Name string `yaml:"name"`
	// Description describes what the tool does
	Description string `yaml:"description"`
	// InputSchema defines the expected input schema (JSON Schema)
	InputSchema map[string]interface{} `yaml:"input_schema"`
	// Responses defines possible responses for this tool
	Responses []ToolResponse `yaml:"responses"`
	// EchoToken makes the tool return the bearer token it was invoked with and
	// its decoded claims (sub, act, groups, aud, iss). Used to assert that a
	// downstream backend accepts a broker-minted token end-to-end.
	EchoToken bool `yaml:"echo_token,omitempty"`
	// EchoHandshake makes the tool return the protocolVersion and client name
	// the connecting MCP client sent in its initialize request, alongside the
	// configured response. Used to assert the protocol revision muster's
	// outbound client negotiates with a downstream server.
	EchoHandshake bool `yaml:"echo_handshake,omitempty"`
}

// ResourceConfig defines a static MCP resource served by the mock server.
//
// Resources exist in the mock so scenarios can exercise the aggregator's
// resource paths -- notably that a URI carrying a scheme is exposed unprefixed,
// which lets two servers advertise the same URI.
type ResourceConfig struct {
	// URI is the resource identifier, e.g. "board://schema"
	URI string `yaml:"uri"`
	// Name is the human-readable resource name
	Name string `yaml:"name"`
	// Description describes what the resource holds
	Description string `yaml:"description"`
	// MIMEType of the resource contents, defaulting to text/plain
	MIMEType string `yaml:"mime_type"`
	// Text is the resource body returned on read
	Text string `yaml:"text"`
}

// ToolResponse defines a conditional response for a mock tool
type ToolResponse struct {
	// Condition defines arg matching for this response (optional)
	// If empty, this response is used as a fallback
	Condition map[string]interface{} `yaml:"condition,omitempty"`
	// Response is the response data to return
	Response interface{} `yaml:"response,omitempty"`
	// Error is the error message to return instead of response
	Error string `yaml:"error,omitempty"`
	// Delay simulates response latency (e.g., "2s", "500ms")
	Delay string `yaml:"delay,omitempty"`
}

// TestScenario is needed for loadServerConfig function - importing from parent would cause circular dependency
// This is a minimal copy of the required fields
type TestScenario struct {
	PreConfiguration *PreConfiguration `yaml:"pre_configuration,omitempty"`
}

// PreConfiguration is a minimal copy needed for the mock server
type PreConfiguration struct {
	MCPServers []MCPServerConfig `yaml:"mcp_servers,omitempty"`
}

// MCPServerConfig is a minimal copy needed for the mock server
type MCPServerConfig struct {
	Name   string                 `yaml:"name"`
	Config map[string]interface{} `yaml:"config"`
}
