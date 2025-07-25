package cli

import (
	"fmt"
	"net/http"
	"time"

	"muster/internal/config"
)

// GetAggregatorEndpoint detects and returns the aggregator endpoint URL from a specific configuration.
//
// Args:
//   - cfg: Pointer to MusterConfig to use for detecting the endpoint.
//
// Returns:
//   - string: The complete HTTP endpoint URL (e.g., "http://localhost:8090/mcp")
//   - error: Always nil (kept for future compatibility)
func GetAggregatorEndpoint(cfg *config.MusterConfig) string {
	defaults := config.GetDefaultConfigWithRoles()
	host := defaults.Aggregator.Host
	port := defaults.Aggregator.Port
	transport := defaults.Aggregator.Transport
	if cfg != nil {
		if cfg.Aggregator.Host != "" && cfg.Aggregator.Host != "0.0.0.0" && cfg.Aggregator.Host != "::" {
			// from here, this is a connectable name/address, not a bind address
			host = cfg.Aggregator.Host
		}
		if cfg.Aggregator.Port != 0 {
			// from here, this is a connectable port, not a bind port
			port = cfg.Aggregator.Port
		}
		transport = cfg.Aggregator.Transport
	}

	if transport == "sse" {
		return fmt.Sprintf("http://%s:%d/sse", host, port)
	}
	return fmt.Sprintf("http://%s:%d/mcp", host, port)
}

// CheckServerRunning verifies that the muster aggregator server is running and responsive.
// It performs a health check by sending a GET request to the MCP endpoint and validates
// the response status code. This is typically used before attempting to execute commands
// that require server connectivity.
//
// Returns:
//   - error: nil if server is running and responsive, otherwise an error with guidance
func CheckServerRunning(endpoint string) error {
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
