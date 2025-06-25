package app

import (
	"muster/internal/config"
)

// Config holds the application configuration
type Config struct {
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
func NewConfig(debug, yolo bool, configPath string) *Config {
	return &Config{
		Debug:      debug,
		Yolo:       yolo,
		ConfigPath: configPath,
	}
}