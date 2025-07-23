package cli

import (
	"fmt"
	"net/http"
	"time"

	"muster/internal/config"
)

// DetectAggregatorEndpoint detects and returns the aggregator endpoint URL from configuration.
// It loads the muster configuration and constructs the MCP endpoint URL using the configured
// host and port settings. If configuration cannot be loaded, it returns a default localhost endpoint.
//
// Returns:
//   - string: The complete HTTP endpoint URL (e.g., "http://localhost:8090/mcp")
//   - error: Always nil (kept for future compatibility)
func DetectAggregatorEndpoint() (string, error) {
	return DetectAggregatorEndpointWithConfig(nil)
}

// DetectAggregatorEndpointWithConfig detects and returns the aggregator endpoint URL from configuration.
// If cfg is provided, it uses that configuration; otherwise it loads the configuration from the system.
// This function is designed to be testable by accepting a configuration arg.
//
// Args:
//   - cfg: Optional configuration to use. If nil, loads from system
//
// Returns:
//   - string: The complete HTTP endpoint URL (e.g., "http://localhost:8090/mcp")
//   - error: Always nil (kept for future compatibility)
func DetectAggregatorEndpointWithConfig(cfg *config.MusterConfig) (string, error) {
	var actualCfg config.MusterConfig

	if cfg != nil {
		actualCfg = *cfg
	} else {
		// Load configuration to get aggregator settings
		actualCfg, _ = config.LoadConfig()
	}

	// Build endpoint from config
	host := actualCfg.Aggregator.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		// from here, this is a connectable name/address, not a bind address
		host = "localhost"
	}
	port := actualCfg.Aggregator.Port
	if port == 0 {
		// from here, this is a connectable port, not a bind port
		port = 8090
	}
	endpoint := fmt.Sprintf("http://%s:%d/mcp", host, port)

	return endpoint, nil
}

// DetectAggregatorEndpointFromPath detects and returns the aggregator endpoint URL from a specific configuration path.
// If configPath is empty, it uses the default configuration loading.
//
// Args:
//   - configPath: Optional custom configuration directory path. If empty, uses default loading
//
// Returns:
//   - string: The complete HTTP endpoint URL (e.g., "http://localhost:8090/mcp")
//   - error: Always nil (kept for future compatibility)
func DetectAggregatorEndpointFromPath(configPath string) (string, error) {

	if configPath == "" {
		panic("Logic error: empty agent configPath")
	}

	cfg, err := config.LoadConfigFromPath(configPath)
	endpoint, err := DetectAggregatorEndpointWithConfig(&cfg)

	return endpoint, err
}

// CheckServerRunning verifies that the muster aggregator server is running and responsive.
// It performs a health check by sending a GET request to the MCP endpoint and validates
// the response status code. This is typically used before attempting to execute commands
// that require server connectivity.
//
// Returns:
//   - error: nil if server is running and responsive, otherwise an error with guidance
func CheckServerRunning() error {
	endpoint, err := DetectAggregatorEndpoint()
	if err != nil {
		return fmt.Errorf("failed to detect endpoint: %w", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Test the MCP endpoint directly with a GET request
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("muster server is not running. Start it with: muster serve")
	}
	defer resp.Body.Close()

	// For streamable-http MCP, a GET request should return 202 Accepted or similar
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("muster server is not responding correctly (status: %d). Try restarting with: muster serve", resp.StatusCode)
	}

	return nil
}
