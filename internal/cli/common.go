package cli

import (
	"fmt"
	"muster/internal/config"
	"net/http"
	"time"
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
	var err error

	if cfg != nil {
		actualCfg = *cfg
	} else {
		// Load configuration to get aggregator settings
		actualCfg, err = config.LoadConfig()
		if err != nil {
			// Use default if config cannot be loaded
			endpoint := "http://localhost:8090/mcp"
			return endpoint, nil
		}
	}

	// Build endpoint from config
	host := actualCfg.Aggregator.Host
	if host == "" {
		host = "localhost"
	}
	port := actualCfg.Aggregator.Port
	if port == 0 {
		port = 8090
	}
	endpoint := fmt.Sprintf("http://%s:%d/mcp", host, port)

	return endpoint, nil
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

// FormatError formats an error message for consistent CLI output display.
// It prefixes the error message with "Error: " for clear identification.
//
// Args:
//   - err: The error to format
//
// Returns:
//   - string: Formatted error message with "Error: " prefix
func FormatError(err error) string {
	return fmt.Sprintf("Error: %v", err)
}

// FormatSuccess formats a success message for CLI output with a checkmark icon.
// Used to provide positive feedback to users when operations complete successfully.
//
// Args:
//   - msg: The success message to format
//
// Returns:
//   - string: Formatted success message with "✓ " prefix
func FormatSuccess(msg string) string {
	return fmt.Sprintf("✓ %s", msg)
}

// FormatWarning formats a warning message for CLI output with a warning icon.
// Used to alert users about potential issues or important information.
//
// Args:
//   - msg: The warning message to format
//
// Returns:
//   - string: Formatted warning message with "⚠ " prefix
func FormatWarning(msg string) string {
	return fmt.Sprintf("⚠ %s", msg)
}
