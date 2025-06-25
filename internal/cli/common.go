package cli

import (
	"fmt"
	"muster/internal/config"
	"net/http"
	"time"
)

// DetectAggregatorEndpoint detects the aggregator endpoint from configuration
func DetectAggregatorEndpoint() (string, error) {
	// Load configuration to get aggregator settings
	cfg, err := config.LoadConfig()
	if err != nil {
		// Use default if config cannot be loaded
		endpoint := "http://localhost:8090/mcp"
		return endpoint, nil
	}

	// Build endpoint from config
	host := cfg.Aggregator.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Aggregator.Port
	if port == 0 {
		port = 8090
	}
	endpoint := fmt.Sprintf("http://%s:%d/mcp", host, port)

	return endpoint, nil
}

// CheckServerRunning checks if the aggregator server is running
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

// FormatError formats an error message for CLI output
func FormatError(err error) string {
	return fmt.Sprintf("Error: %v", err)
}

// FormatSuccess formats a success message for CLI output
func FormatSuccess(msg string) string {
	return fmt.Sprintf("✓ %s", msg)
}

// FormatWarning formats a warning message for CLI output
func FormatWarning(msg string) string {
	return fmt.Sprintf("⚠ %s", msg)
}
