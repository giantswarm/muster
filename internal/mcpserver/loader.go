package mcpserver

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"muster/internal/api"
	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

// LoadDefinitions loads MCP server definitions from a directory
func LoadDefinitions(configPath string) ([]api.MCPServer, error) {
	if configPath == "" {
		return []api.MCPServer{}, nil
	}

	// Check if path exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logging.Warn("MCPServerLoader", "Config path does not exist: %s", configPath)
		return []api.MCPServer{}, nil
	}

	var definitions []api.MCPServer

	// Walk through the directory
	err := filepath.Walk(configPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process YAML files
		if !strings.HasSuffix(strings.ToLower(path), ".yaml") && !strings.HasSuffix(strings.ToLower(path), ".yml") {
			return nil
		}

		// Load the file
		def, err := LoadDefinitionFromFile(path)
		if err != nil {
			logging.Error("MCPServerLoader", err, "Failed to load definition from %s", path)
			return nil // Continue with other files
		}

		if def != nil {
			definitions = append(definitions, *def)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk config directory: %w", err)
	}

	logging.Info("MCPServerLoader", "Loaded %d MCP server definitions from %s", len(definitions), configPath)
	return definitions, nil
}

// LoadDefinitionFromFile loads a single MCP server definition from a YAML file
func LoadDefinitionFromFile(filepath string) (*api.MCPServer, error) {
	content, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filepath, err)
	}

	var def api.MCPServer
	if err := yaml.Unmarshal(content, &def); err != nil {
		return nil, fmt.Errorf("failed to parse YAML from %s: %w", filepath, err)
	}

	logging.Debug("MCPServerLoader", "Loaded definition for %s from %s", def.Name, filepath)
	return &def, nil
}

// SaveDefinitionToFile saves an MCP server definition to a YAML file
func SaveDefinitionToFile(def *api.MCPServer, filepath string) error {
	content, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal definition: %w", err)
	}

	if err := ioutil.WriteFile(filepath, content, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filepath, err)
	}

	logging.Debug("MCPServerLoader", "Saved definition for %s to %s", def.Name, filepath)
	return nil
}
