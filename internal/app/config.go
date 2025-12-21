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

	// OAuth Proxy settings
	OAuthEnabled   bool   // Enable OAuth proxy for remote MCP server authentication
	OAuthPublicURL string // Publicly accessible URL of the Muster Server
	OAuthClientID  string // OAuth client identifier (CIMD URL)
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
