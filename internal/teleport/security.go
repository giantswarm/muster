package teleport

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Security-related constants and validation functions.

// AllowedNamespaces defines the namespaces from which Teleport identity secrets
// can be loaded. This provides defense-in-depth against misconfiguration.
// An empty list means all namespaces are allowed (default for backward compatibility).
//
// The muster pod's own namespace (resolved from K8S_NAMESPACE / POD_NAMESPACE
// at process start) is always allowed in addition to this list — that's where
// tbot writes its identity Secrets via the chart's `kubernetes_secret`
// destination, and `targetNamespace: muster` is the standard chart layout.
// Without it, Teleport-routed MCPServers fail their autoStart probe with
// `namespace "muster" is not in the allowed list` whenever the chart is
// installed under any namespace other than `muster-system`.
var AllowedNamespaces = []string{
	"teleport-system",
	"muster-system",
}

// appNamePattern defines the valid pattern for Teleport application names.
// Only alphanumeric characters, hyphens, underscores, and dots are allowed.
// This prevents potential header injection attacks.
var appNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// MaxAppNameLength is the maximum allowed length for app names.
const MaxAppNameLength = 253 // DNS subdomain max length

// ValidateAppName validates a Teleport application name to ensure it's safe
// for use in HTTP Host headers.
//
// Valid app names:
//   - Start with alphanumeric character
//   - Contain only alphanumeric, hyphens, underscores, and dots
//   - Maximum 253 characters (DNS subdomain limit)
//
// Returns an error if the app name is invalid.
func ValidateAppName(appName string) error {
	if appName == "" {
		return nil // Empty is valid (optional field)
	}

	if len(appName) > MaxAppNameLength {
		return fmt.Errorf("app name exceeds maximum length of %d characters", MaxAppNameLength)
	}

	if !appNamePattern.MatchString(appName) {
		return fmt.Errorf("app name contains invalid characters: must start with alphanumeric and contain only alphanumeric, hyphens, underscores, and dots")
	}

	return nil
}

// ValidateIdentityDir validates and sanitizes an identity directory path.
//
// Security checks:
//   - Must be an absolute path
//   - Cannot contain path traversal sequences (..)
//   - Cleaned and normalized
//
// Returns the cleaned path or an error if validation fails.
func ValidateIdentityDir(identityDir string) (string, error) {
	if identityDir == "" {
		return "", fmt.Errorf("identity directory cannot be empty")
	}

	// Clean the path to resolve any . or .. components
	cleaned := filepath.Clean(identityDir)

	// Must be an absolute path
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("identity directory must be an absolute path: %s", identityDir)
	}

	// After cleaning, check that the original didn't contain traversal attempts
	// that would be resolved differently
	if strings.Contains(identityDir, "..") {
		return "", fmt.Errorf("identity directory cannot contain path traversal sequences: %s", identityDir)
	}

	return cleaned, nil
}

// ValidateNamespace validates a Kubernetes namespace for secret access.
//
// If AllowedNamespaces is empty, all namespaces are allowed.
// Otherwise, the namespace must be in the allowed list.
//
// Special case: if the namespace matches the MCPServer's own namespace,
// it's always allowed (use ValidateNamespaceWithOwner for this).
func ValidateNamespace(namespace string) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}

	// The pod's own namespace is always allowed — it's where the chart's
	// `kubernetes_secret` tbot output writes identity Secrets and is the
	// natural destination for chart-installed deployments. Resolved at
	// each call so test setups that mutate the env var pick it up.
	if podNS := podNamespace(); podNS != "" && namespace == podNS {
		return nil
	}

	// If no restrictions are configured, allow all
	if len(AllowedNamespaces) == 0 {
		return nil
	}

	for _, allowed := range AllowedNamespaces {
		if namespace == allowed {
			return nil
		}
	}

	return fmt.Errorf("namespace %q is not in the allowed list for Teleport identity secrets", namespace)
}

// podNamespace returns the muster pod's own namespace, resolved from the
// downward-API env vars set by the chart (K8S_NAMESPACE on the muster
// container, POD_NAMESPACE on the init container and several operator-style
// charts). Returns empty when running outside Kubernetes.
func podNamespace() string {
	if ns := os.Getenv("K8S_NAMESPACE"); ns != "" {
		return ns
	}
	return os.Getenv("POD_NAMESPACE")
}

// SecretNamePattern defines the valid pattern for Kubernetes secret names.
var secretNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// MaxSecretNameLength is the maximum length for Kubernetes resource names.
const MaxSecretNameLength = 253

// ValidateSecretName validates a Kubernetes secret name.
func ValidateSecretName(name string) error {
	if name == "" {
		return fmt.Errorf("secret name cannot be empty")
	}

	if len(name) > MaxSecretNameLength {
		return fmt.Errorf("secret name exceeds maximum length of %d characters", MaxSecretNameLength)
	}

	if !secretNamePattern.MatchString(name) {
		return fmt.Errorf("secret name %q is invalid: must be lowercase alphanumeric with hyphens and dots", name)
	}

	return nil
}
