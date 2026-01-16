package cli

import (
	"os"

	musterctx "muster/internal/context"
)

// ContextEnvVar is the environment variable name for overriding the current context.
const ContextEnvVar = musterctx.ContextEnvVar

// ResolveEndpoint resolves the endpoint URL using the precedence order:
// 1. Explicit endpoint (from --endpoint flag)
// 2. Context name (from --context flag)
// 3. MUSTER_CONTEXT environment variable
// 4. current-context from contexts.yaml
// 5. Empty string (caller should fall back to config-based endpoint)
//
// Returns the resolved endpoint URL and any error encountered.
// If no context is configured, returns an empty string (not an error).
func ResolveEndpoint(explicitEndpoint, contextName string) (string, error) {
	// 1. Explicit endpoint takes highest precedence
	if explicitEndpoint != "" {
		return explicitEndpoint, nil
	}

	// 2. Check for --context flag
	if contextName != "" {
		return getEndpointFromContext(contextName)
	}

	// 3. Check for MUSTER_CONTEXT environment variable
	if envContext := os.Getenv(ContextEnvVar); envContext != "" {
		return getEndpointFromContext(envContext)
	}

	// 4. Check for current-context in contexts.yaml
	storage, err := musterctx.NewStorage()
	if err != nil {
		// Storage initialization failed - fall back to config-based resolution
		return "", nil
	}

	ctx, err := storage.GetCurrentContext()
	if err != nil {
		// Failed to get current context - fall back to config-based resolution
		return "", nil
	}

	if ctx != nil {
		return ctx.Endpoint, nil
	}

	// 5. No context configured - return empty string for config-based fallback
	return "", nil
}

// getEndpointFromContext retrieves the endpoint for a named context.
func getEndpointFromContext(contextName string) (string, error) {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return "", err
	}

	ctx, err := storage.GetContext(contextName)
	if err != nil {
		return "", err
	}

	if ctx == nil {
		return "", &musterctx.ContextNotFoundError{Name: contextName}
	}

	return ctx.Endpoint, nil
}

// GetContextSettings returns the settings for the current or specified context.
// Returns nil if no context is configured or the context has no settings.
func GetContextSettings(contextName string) *musterctx.ContextSettings {
	storage, err := musterctx.NewStorage()
	if err != nil {
		return nil
	}

	var ctx *musterctx.Context

	if contextName != "" {
		ctx, _ = storage.GetContext(contextName)
	} else if envContext := os.Getenv(ContextEnvVar); envContext != "" {
		ctx, _ = storage.GetContext(envContext)
	} else {
		ctx, _ = storage.GetCurrentContext()
	}

	if ctx != nil {
		return ctx.Settings
	}

	return nil
}
