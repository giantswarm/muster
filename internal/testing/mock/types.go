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
}

// ToolResponse defines a conditional response for a mock tool
type ToolResponse struct {
	// Condition defines parameter matching for this response (optional)
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
