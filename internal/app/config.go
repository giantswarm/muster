package app

import (
	"muster/internal/config"
)

// Config holds the application configuration that controls bootstrap behavior and execution modes.
// This struct encapsulates all settings needed during application initialization and runtime,
// including UI preferences, debugging options, safety settings, and configuration loading behavior.
//
// The configuration supports both layered and single-path configuration loading strategies:
//   - Layered: Merges configuration from defaults, user config, and project config
//   - Single-path: Loads configuration from a specific directory only
//
// Field descriptions:
//   - NoTUI: When true, runs in CLI mode; when false, runs in TUI mode
//   - Debug: Enables debug-level logging and additional diagnostic output
//   - Yolo: Enables "you only live once" mode with relaxed safety checks
//   - ConfigPath: Optional custom configuration directory path
//   - MusterConfig: Loaded muster configuration (populated during bootstrap)
type Config struct {
	// Version is the muster build version (e.g., "v0.1.0").
	// This is passed to the aggregator to report in the MCP protocol handshake.
	Version string

	// Debug settings
	Debug bool

	// Silent disables all output to the console.
	Silent bool

	// Yolo enables "you only live once" mode with relaxed safety checks.
	// This setting reduces confirmation prompts and safety validations.
	// Use with caution in production environments.
	Yolo bool

	// ConfigPath specifies a custom configuration directory path.
	// When set, disables layered configuration loading and loads from this path only.
	// When empty, uses standard layered configuration loading strategy.
	ConfigPath string

	// MusterConfig holds the loaded muster environment configuration.
	// This field is populated during application bootstrap after configuration loading.
	MusterConfig *config.MusterConfig

	// OAuth Proxy settings (ADR 004 - for authenticating to remote MCP servers)
	OAuthEnabled   bool   // Enable OAuth proxy for remote MCP server authentication
	OAuthPublicURL string // Publicly accessible URL of the Muster Server
	OAuthClientID  string // OAuth client identifier (CIMD URL)

	// OAuth Server settings (ADR 005 - for protecting the Muster Server)
	OAuthServerEnabled bool   // Enable OAuth 2.1 protection for Muster Server
	OAuthServerBaseURL string // Base URL of the Muster Server for OAuth issuer
}

// NewConfig creates a new application configuration with the specified settings.
// This is the primary constructor for application configuration, taking all
// essential runtime args needed for application bootstrap and execution.
//
// Args:
//   - debug: enables debug logging and verbose output
//   - silent: disables all output to the console
//   - yolo: enables relaxed safety checks and reduced confirmations
//   - configPath: custom config directory (empty string for default layered loading)
//
// Returns a fully initialized Config struct ready for use with NewApplication.
//
// Example:
//
//	// Standard mode with debug enabled
//	cfg := app.NewConfig(true, false, false, "")
//
//	// Custom configuration path
//	cfg := app.NewConfig(false, false, false, "/opt/muster/config")
func NewConfig(debug, silent, yolo bool, configPath string) *Config {
	return &Config{
		Debug:      debug,
		Silent:     silent,
		Yolo:       yolo,
		ConfigPath: configPath,
	}
}

// WithOAuth adds OAuth proxy configuration to the Config.
// This method enables OAuth proxy functionality and sets the required parameters.
//
// Args:
//   - enabled: whether OAuth proxy is enabled
//   - publicURL: the publicly accessible URL of the Muster Server
//   - clientID: the OAuth client identifier (CIMD URL)
//
// Returns the modified Config for method chaining.
func (c *Config) WithOAuth(enabled bool, publicURL, clientID string) *Config {
	c.OAuthEnabled = enabled
	c.OAuthPublicURL = publicURL
	c.OAuthClientID = clientID
	return c
}

// WithOAuthServer adds OAuth server protection configuration to the Config.
// This method enables OAuth 2.1 protection for the Muster Server itself (ADR 005).
// When enabled, clients (like muster agent) must authenticate with OAuth before
// accessing MCP endpoints.
//
// Note: Full OAuth server configuration (provider, storage, etc.) should be
// done via the config file. These flags provide convenience overrides for
// enabling the feature and setting the base URL.
//
// Args:
//   - enabled: whether OAuth server protection is enabled
//   - baseURL: the base URL of the Muster Server (used as OAuth issuer)
//
// Returns the modified Config for method chaining.
func (c *Config) WithOAuthServer(enabled bool, baseURL string) *Config {
	c.OAuthServerEnabled = enabled
	c.OAuthServerBaseURL = baseURL
	return c
}

// WithVersion sets the muster build version on the Config.
// This version is passed to the aggregator to report in the MCP protocol handshake,
// allowing clients to discover the server version during initialization.
//
// Args:
//   - version: the build version string (e.g., "v0.1.0", "dev")
//
// Returns the modified Config for method chaining.
func (c *Config) WithVersion(version string) *Config {
	c.Version = version
	return c
}
