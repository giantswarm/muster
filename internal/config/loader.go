package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

const (
	userConfigDir  = ".config/muster"
	configFileName = "config.yaml"
)

func GetDefaultConfigPathOrPanic() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not determine user config directory: %w", err))
	}

	return filepath.Join(homeDir, userConfigDir)
}

// LoadConfig loads configuration from a single specified directory.
// The directory should contain config.yaml and subdirectories for other configuration types.
func LoadConfig(configPath string) (MusterConfig, error) {
	// Load main config.yaml from the specified path
	configFilePath := filepath.Join(configPath, configFileName)
	config := GetDefaultConfigWithRoles() // Start with default config

	// Start with default config
	data, err := os.ReadFile(configFilePath)
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

// resolveSecretFiles reads secrets from file paths specified in *File config options.
// This is the recommended way to handle secrets in production, keeping them out of
// config files and environment variables (per MCP OAuth security recommendations).
func resolveSecretFiles(config *MusterConfig) error {
	oauthServer := &config.Aggregator.OAuthServer

	// Dex client secret
	if oauthServer.Dex.ClientSecretFile != "" && oauthServer.Dex.ClientSecret == "" {
		secret, err := readSecretFile(oauthServer.Dex.ClientSecretFile)
		if err != nil {
			return fmt.Errorf("failed to read Dex client secret from %s: %w", oauthServer.Dex.ClientSecretFile, err)
		}
		oauthServer.Dex.ClientSecret = secret
		logging.Info("ConfigLoader", "Loaded Dex client secret from file")
	}

	// Google client secret
	if oauthServer.Google.ClientSecretFile != "" && oauthServer.Google.ClientSecret == "" {
		secret, err := readSecretFile(oauthServer.Google.ClientSecretFile)
		if err != nil {
			return fmt.Errorf("failed to read Google client secret from %s: %w", oauthServer.Google.ClientSecretFile, err)
		}
		oauthServer.Google.ClientSecret = secret
		logging.Info("ConfigLoader", "Loaded Google client secret from file")
	}

	// Registration token
	if oauthServer.RegistrationTokenFile != "" && oauthServer.RegistrationToken == "" {
		secret, err := readSecretFile(oauthServer.RegistrationTokenFile)
		if err != nil {
			return fmt.Errorf("failed to read registration token from %s: %w", oauthServer.RegistrationTokenFile, err)
		}
		oauthServer.RegistrationToken = secret
		logging.Info("ConfigLoader", "Loaded registration token from file")
	}

	// Encryption key
	if oauthServer.EncryptionKeyFile != "" && oauthServer.EncryptionKey == "" {
		secret, err := readSecretFile(oauthServer.EncryptionKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read encryption key from %s: %w", oauthServer.EncryptionKeyFile, err)
		}
		oauthServer.EncryptionKey = secret
		logging.Info("ConfigLoader", "Loaded encryption key from file")
	}

	// Valkey password
	if oauthServer.Storage.Valkey.PasswordFile != "" && oauthServer.Storage.Valkey.Password == "" {
		secret, err := readSecretFile(oauthServer.Storage.Valkey.PasswordFile)
		if err != nil {
			return fmt.Errorf("failed to read Valkey password from %s: %w", oauthServer.Storage.Valkey.PasswordFile, err)
		}
		oauthServer.Storage.Valkey.Password = secret
		logging.Info("ConfigLoader", "Loaded Valkey password from file")
	}

	return nil
}

// readSecretFile reads a secret from a file, trimming any trailing whitespace.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// Trim trailing whitespace/newlines which are common in mounted secrets
	return strings.TrimSpace(string(data)), nil
}
