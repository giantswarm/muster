package config

// GetDefaultConfigWithRoles returns default configuration
func GetDefaultConfigWithRoles() MusterConfig {
	return MusterConfig{
		GlobalSettings: GlobalSettings{
			DefaultContainerRuntime: "docker",
		},
		Aggregator: AggregatorConfig{
			Port:      8090,
			Host:      "localhost",
			Transport: MCPTransportStreamableHTTP,
			Enabled:   true,
		},
	}
}
