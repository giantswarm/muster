package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/giantswarm/muster/internal/config"
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

// ParseTimeFilter parses time strings in various formats for event filtering and other time-based queries.
// It supports duration format (e.g., "1h", "30m"), RFC3339 format (e.g., "2024-01-15T10:00:00Z"),
// date-only format (e.g., "2024-01-15"), and datetime format (e.g., "2024-01-15 10:00:00").
//
// For duration format, the returned time is calculated as now minus the duration.
//
// Args:
//   - timeStr: The time string to parse
//
// Returns:
//   - time.Time: The parsed time
//   - error: An error if the format is not recognized
func ParseTimeFilter(timeStr string) (time.Time, error) {
	// Try duration format first (e.g., "1h", "30m", "2h30m")
	if duration, err := time.ParseDuration(timeStr); err == nil {
		return time.Now().Add(-duration), nil
	}

	// Try RFC3339 format (e.g., "2024-01-15T10:00:00Z")
	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t, nil
	}

	// Try date-only format (e.g., "2024-01-15")
	if t, err := time.Parse("2006-01-02", timeStr); err == nil {
		return t, nil
	}

	// Try date-time format without timezone (e.g., "2024-01-15 10:00:00")
	if t, err := time.Parse("2006-01-02 15:04:05", timeStr); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unsupported time format '%s'. Supported formats: duration (1h, 30m), RFC3339 (2024-01-15T10:00:00Z), date (2024-01-15), or datetime (2024-01-15 10:00:00)", timeStr)
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

// IsRemoteEndpoint checks if an endpoint URL points to a remote server.
// It properly parses the URL and checks only the hostname, avoiding false positives
// when "localhost" appears in the path or query string.
//
// Args:
//   - endpoint: The endpoint URL to check
//
// Returns:
//   - bool: true if the endpoint is remote (not localhost), false otherwise
func IsRemoteEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		// If we can't parse the URL, assume it's remote for safety
		return true
	}

	host := strings.ToLower(u.Hostname())
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}
