package app

import (
	"testing"
)

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name       string
		silent     bool
		debug      bool
		yolo       bool
		configPath string
	}{
		{
			name:       "full configuration",
			silent:     true,
			debug:      true,
			yolo:       true,
			configPath: "/custom/config/path",
		},
		{
			name:       "minimal configuration",
			debug:      false,
			silent:     false,
			yolo:       false,
			configPath: "",
		},
		{
			name:       "debug only",
			silent:     false,
			debug:      true,
			yolo:       false,
			configPath: "",
		},
		{
			name:       "with custom config path",
			silent:     false,
			debug:      false,
			yolo:       false,
			configPath: "/test/config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig(tt.debug, tt.silent, tt.yolo, tt.configPath)

			if cfg.Debug != tt.debug {
				t.Errorf("Debug = %v, want %v", cfg.Debug, tt.debug)
			}
			if cfg.Silent != tt.silent {
				t.Errorf("Silent = %v, want %v", cfg.Silent, tt.silent)
			}
			if cfg.Yolo != tt.yolo {
				t.Errorf("Yolo = %v, want %v", cfg.Yolo, tt.yolo)
			}
			if cfg.ConfigPath != tt.configPath {
				t.Errorf("ConfigPath = %v, want %v", cfg.ConfigPath, tt.configPath)
			}
			if cfg.MusterConfig != nil {
				t.Error("MusterConfig should be nil before loading")
			}
		})
	}
}

func TestConfigFields(t *testing.T) {
	// Test that all fields can be set and retrieved
	cfg := &Config{
		Debug: true,
		Yolo:  true,
	}

	if !cfg.Debug {
		t.Error("Debug should be true")
	}
	if !cfg.Yolo {
		t.Error("Yolo should be true")
	}
}
