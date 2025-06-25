package app

import (
	"muster/internal/config"
)

// Config holds the application configuration
type Config struct {
	// UI mode
	NoTUI bool

	// Debug settings
	Debug bool

	// Safety settings
	Yolo bool

	// Custom configuration path (optional)
	// When set, disables layered configuration loading
	ConfigPath string

	// Environment configuration
	MusterConfig *config.MusterConfig
}

// NewConfig creates a new application configuration
func NewConfig(noTUI, debug, yolo bool, configPath string) *Config {
	return &Config{
		NoTUI:      noTUI,
		Debug:      debug,
		Yolo:       yolo,
		ConfigPath: configPath,
	}
}
