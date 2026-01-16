package context

import (
	"fmt"
	"regexp"
)

// ContextEnvVar is the environment variable name for overriding the current context.
const ContextEnvVar = "MUSTER_CONTEXT"

// maxContextNameLength is the maximum allowed length for context names.
// This follows Kubernetes label constraints for consistency.
const maxContextNameLength = 63

// contextNamePattern defines valid context name characters.
// Context names must be lowercase alphanumeric with hyphens allowed.
var contextNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)

// ContextSettings contains optional per-context settings.
// These settings override global defaults when using a specific context.
type ContextSettings struct {
	// Output is the default output format for this context (table, json, yaml)
	Output string `yaml:"output,omitempty"`
}

// Context represents a named muster endpoint configuration.
// Each context provides a convenient alias for a muster aggregator URL.
type Context struct {
	// Name is the unique identifier for this context
	Name string `yaml:"name"`
	// Endpoint is the full URL to the muster aggregator (e.g., http://localhost:8090/mcp)
	Endpoint string `yaml:"endpoint"`
	// Settings contains optional context-specific settings
	Settings *ContextSettings `yaml:"settings,omitempty"`
}

// ContextConfig represents the complete contexts configuration file.
// This is the root structure stored in ~/.config/muster/contexts.yaml.
type ContextConfig struct {
	// CurrentContext is the name of the currently active context
	CurrentContext string `yaml:"current-context,omitempty"`
	// Contexts is the list of all defined contexts
	Contexts []Context `yaml:"contexts,omitempty"`
}

// ValidateContextName validates a context name according to the naming rules.
// Context names must:
//   - Be between 1 and 63 characters
//   - Contain only lowercase letters, numbers, and hyphens
//   - Start and end with an alphanumeric character
//
// Returns an error if the name is invalid.
func ValidateContextName(name string) error {
	if name == "" {
		return fmt.Errorf("context name cannot be empty")
	}

	if len(name) > maxContextNameLength {
		return fmt.Errorf("context name cannot exceed %d characters", maxContextNameLength)
	}

	if !contextNamePattern.MatchString(name) {
		return fmt.Errorf("context name must contain only lowercase letters, numbers, and hyphens, and must start and end with an alphanumeric character")
	}

	return nil
}

// GetContext returns the context with the given name, or nil if not found.
func (c *ContextConfig) GetContext(name string) *Context {
	for i := range c.Contexts {
		if c.Contexts[i].Name == name {
			return &c.Contexts[i]
		}
	}
	return nil
}

// HasContext returns true if a context with the given name exists.
func (c *ContextConfig) HasContext(name string) bool {
	return c.GetContext(name) != nil
}

// AddOrUpdateContext adds a new context or updates an existing one.
// If a context with the same name exists, it will be replaced.
func (c *ContextConfig) AddOrUpdateContext(ctx Context) {
	for i := range c.Contexts {
		if c.Contexts[i].Name == ctx.Name {
			c.Contexts[i] = ctx
			return
		}
	}
	c.Contexts = append(c.Contexts, ctx)
}

// RemoveContext removes the context with the given name.
// Returns true if the context was found and removed, false otherwise.
// If the removed context was the current context, CurrentContext is cleared.
func (c *ContextConfig) RemoveContext(name string) bool {
	for i := range c.Contexts {
		if c.Contexts[i].Name == name {
			c.Contexts = append(c.Contexts[:i], c.Contexts[i+1:]...)
			if c.CurrentContext == name {
				c.CurrentContext = ""
			}
			return true
		}
	}
	return false
}
