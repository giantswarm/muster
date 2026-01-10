package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// CheckEndpointSystemdSocket verifies that the muster endpoint configuration matches a potential systemd socket
//
// Args:
//   - cfg: Pointer to MusterConfig to use for checking possible endpoints.
//
// This function checks if the systemd user mode socket file exists and contains the expected endpoint.
// returns true if systemd socket activation isn't used or the endpoint matches, false otherwise.
func CheckEndpointSystemdSocket(cfg *config.MusterConfig) bool {

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return true
	}

	// systemd structure (if used) is well-known
	musterSocketLink := filepath.Join(homeDir, ".config/systemd/user/sockets.target.wants/muster.socket")
	socketFile, err := os.ReadFile(musterSocketLink)
	if err != nil {
		return true // systemd (user mode) socket activation very likely isn't used
	}

	defaultEndpoint := GetAggregatorEndpoint(cfg)
	endpoints := []string{defaultEndpoint}
	if cfg != nil {
		// add IPv4 listen address variants reachable via "localhost"
		if cfg.Aggregator.Host == "localhost" ||
			cfg.Aggregator.Host == "127.0.0.1" ||
			cfg.Aggregator.Host == "0.0.0.0" {
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", "localhost", cfg.Aggregator.Port))
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", "127.0.0.1", cfg.Aggregator.Port))
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", "0.0.0.0", cfg.Aggregator.Port))
		}

		// add IPv6 listen address variants reachable via "localhost"
		if cfg.Aggregator.Host == "localhost" ||
			cfg.Aggregator.Host == "ip6-localhost" ||
			cfg.Aggregator.Host == "ip6-loopback" ||
			cfg.Aggregator.Host == "::1" ||
			cfg.Aggregator.Host == "::" {
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", "::1", cfg.Aggregator.Port))
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", "::", cfg.Aggregator.Port))
		}

		// Checking the host's other names or IP addresses would require more complex logic
		// and is not implemented here, as it is not a common use case.
	}

	socketConfig := string(socketFile)
	for _, endpoint := range endpoints {
		if strings.Contains(socketConfig, endpoint) {
			return true // systemd socket enabled and matches the expected endpoint
		}
	}

	return false // systemd socket enabled, but not matching
}
