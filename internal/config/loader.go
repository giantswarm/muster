package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giantswarm/muster/v5/pkg/logging"

	"gopkg.in/yaml.v3"
)

const (
	userConfigDir  = ".config/muster"
	configFileName = "config.yaml"
)

// DefaultConfigPath returns the default configuration directory,
// ~/.config/muster. Without a home directory (HOME unset) it returns an error
// that tells the caller to pass --config-path.
func DefaultConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine the default configuration directory (%w): set HOME or pass --config-path", err)
	}

	return filepath.Join(homeDir, userConfigDir), nil
}

// DefaultConfigPathFlag returns the default of a --config-path flag: the
// default configuration directory, or "" without a home directory, so that
// the binary starts and only a command that needs the directory fails, with
// the error of ResolveConfigPath.
func DefaultConfigPathFlag() string {
	path, err := DefaultConfigPath()
	if err != nil {
		return ""
	}
	return path
}

// ResolveConfigPath returns configPath, or the default configuration
// directory when configPath is empty.
func ResolveConfigPath(configPath string) (string, error) {
	if configPath != "" {
		return configPath, nil
	}
	return DefaultConfigPath()
}

// LoadConfig loads configuration from a single specified directory.
// The directory should contain config.yaml and subdirectories for other configuration types.
// An empty configPath means the default configuration directory.
func LoadConfig(configPath string) (MusterConfig, error) {
	configPath, err := ResolveConfigPath(configPath)
	if err != nil {
		return MusterConfig{}, err
	}
	// Load main config.yaml from the specified path
	configFilePath := filepath.Join(configPath, configFileName)
	config := GetDefaultConfigWithRoles() // Start with default config

	// Start with default config
	data, err := os.ReadFile(configFilePath) //nolint:gosec
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logging.Info("ConfigLoader", "No config.yaml found at %s, using defaults", configFilePath)
			return config, nil
		}
		logging.Info("ConfigLoader", "Error loading config.yaml from %s: %s", configFilePath, err)
		return MusterConfig{}, err
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		// config malformed
		return MusterConfig{}, fmt.Errorf("error loading config from %s: %w", configFilePath, err)
	}
	logging.Info("ConfigLoader", "Loaded configuration from %s", configFilePath)

	// Resolve secrets from files (recommended for production deployments)
	if err := resolveSecretFiles(&config); err != nil {
		return MusterConfig{}, fmt.Errorf("error resolving secret files: %w", err)
	}

	return config, nil
}

// secretMapping defines a secret file to load and where to store it.
type secretMapping struct {
	file   string
	target *string
	name   string
}

// resolveSecretFiles reads secrets from file paths specified in *File config options.
// This is the recommended way to handle secrets in production, keeping them out of
// config files and environment variables (per MCP OAuth security recommendations).
func resolveSecretFiles(config *MusterConfig) error {
	oauthServer := &config.Aggregator.OAuth.Server

	secrets := []secretMapping{
		{oauthServer.Dex.ClientSecretFile, &oauthServer.Dex.ClientSecret, "Dex client secret"},
		{oauthServer.Google.ClientSecretFile, &oauthServer.Google.ClientSecret, "Google client secret"},
		{oauthServer.RegistrationTokenFile, &oauthServer.RegistrationToken, "registration token"},
		{oauthServer.EncryptionKeyFile, &oauthServer.EncryptionKey, "encryption key"},
		{oauthServer.Storage.Valkey.PasswordFile, &oauthServer.Storage.Valkey.Password, "Valkey password"},
	}

	for _, s := range secrets {
		if s.file != "" && *s.target == "" {
			secret, err := readSecretFile(s.file)
			if err != nil {
				return fmt.Errorf("failed to read %s from %s: %w", s.name, s.file, err)
			}
			*s.target = secret
			logging.Info("ConfigLoader", "Loaded %s from file", s.name)
		}
	}

	return nil
}

// readSecretFile reads a secret from a file, trimming any trailing whitespace.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	// Trim trailing whitespace/newlines which are common in mounted secrets
	return strings.TrimSpace(string(data)), nil
}
