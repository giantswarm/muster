package cli

// Server type constants for MCP server classification.
const (
	// ServerTypeStdio represents a local stdio-based MCP server.
	ServerTypeStdio = "stdio"
	// ServerTypeStreamableHTTP represents a remote streamable HTTP MCP server.
	ServerTypeStreamableHTTP = "streamable-http"
	// ServerTypeSSE represents a remote Server-Sent Events MCP server.
	ServerTypeSSE = "sse"
)

// IsRemoteServerType returns true if the server type represents a remote connection.
// Remote servers use network connections (streamable-http or sse) rather than local
// process communication (stdio).
//
// Args:
//   - serverType: The server type string to check
//
// Returns:
//   - bool: true if the server type is remote (streamable-http or sse)
func IsRemoteServerType(serverType string) bool {
	return serverType == ServerTypeStreamableHTTP || serverType == ServerTypeSSE
}

// ExtractServerType extracts the server type from a data map.
// It checks common field names that indicate the server type in MCP server data.
//
// The function checks the following fields in order:
//   - "type": Direct server type field
//   - "metadata": May contain server type as a string
//
// Args:
//   - data: Map containing server data
//
// Returns:
//   - string: The server type (stdio, streamable-http, sse) or empty string if not found
func ExtractServerType(data map[string]interface{}) string {
	if data == nil {
		return ""
	}

	// Check for "type" field (most common)
	if typeVal, exists := data["type"]; exists {
		if typeStr, ok := typeVal.(string); ok {
			return typeStr
		}
	}

	// Check for "metadata" field (used in check/status commands)
	if metadata, exists := data["metadata"]; exists {
		if metaStr, ok := metadata.(string); ok {
			// Metadata may contain the server type directly
			if metaStr == ServerTypeStreamableHTTP || metaStr == ServerTypeSSE || metaStr == ServerTypeStdio {
				return metaStr
			}
		}
	}

	return ""
}

// IsMCPServerData checks if the data represents an MCP server.
// It analyzes the data structure to identify MCP server-specific patterns.
//
// Args:
//   - data: Map containing potential MCP server data
//
// Returns:
//   - bool: true if the data appears to be MCP server related
func IsMCPServerData(data map[string]interface{}) bool {
	if data == nil {
		return false
	}

	// Check for MCP server type indicators
	if typ, exists := data["type"]; exists {
		if typeStr, ok := typ.(string); ok {
			if typeStr == ServerTypeStdio || typeStr == ServerTypeStreamableHTTP || typeStr == ServerTypeSSE {
				return true
			}
		}
	}

	// Check for metadata field containing server type
	if metadata, exists := data["metadata"]; exists {
		if metaStr, ok := metadata.(string); ok {
			if metaStr == ServerTypeStdio || metaStr == ServerTypeStreamableHTTP || metaStr == ServerTypeSSE {
				return true
			}
		}
	}

	// Check for service_type = MCPServer
	if serviceType, exists := data["service_type"]; exists {
		if typeStr, ok := serviceType.(string); ok {
			if typeStr == "MCPServer" || typeStr == "mcpserver" {
				return true
			}
		}
	}

	return false
}
