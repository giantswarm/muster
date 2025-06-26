package agent

import "github.com/mark3labs/mcp-go/mcp"

// showToolDiff displays the differences between old and new tool lists.
// This method is called when tool change notifications are received to provide
// visual feedback about what capabilities have been added or removed from the
// MCP server. It compares the tool lists by name and categorizes changes as
// added, removed, or unchanged.
//
// The diff output uses color-coded logging:
//   - Green (✓) for unchanged tools
//   - Green (+) for newly added tools
//   - Red (-) for removed tools
//
// This method is only called when caching is enabled and a logger is available.
func (c *Client) showToolDiff(oldTools, newTools []mcp.Tool) {
	// Create maps for easier comparison using tool names as keys
	oldMap := make(map[string]mcp.Tool)
	for _, tool := range oldTools {
		oldMap[tool.Name] = tool
	}

	newMap := make(map[string]mcp.Tool)
	for _, tool := range newTools {
		newMap[tool.Name] = tool
	}

	// Check for changes by comparing tool name presence
	var added []string
	var removed []string
	var unchanged []string

	// Find added and unchanged tools
	for name := range newMap {
		if _, exists := oldMap[name]; exists {
			unchanged = append(unchanged, name)
		} else {
			added = append(added, name)
		}
	}

	// Find removed tools
	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			removed = append(removed, name)
		}
	}

	// Display changes with appropriate visual indicators
	if len(added) > 0 || len(removed) > 0 {
		c.logger.Info("Tool changes detected:")
		for _, name := range unchanged {
			c.logger.Success("  ✓ Unchanged: %s", name)
		}
		for _, name := range added {
			c.logger.Success("  + Added: %s", name)
		}
		for _, name := range removed {
			c.logger.Error("  - Removed: %s", name)
		}
	} else {
		c.logger.Info("No tool changes detected")
	}
}

// showResourceDiff displays the differences between old and new resource lists.
// This method is called when resource change notifications are received to provide
// visual feedback about what resources have been added or removed from the
// MCP server. It compares the resource lists by URI and categorizes changes as
// added, removed, or unchanged.
//
// The diff output uses color-coded logging:
//   - Green (✓) for unchanged resources
//   - Green (+) for newly added resources
//   - Red (-) for removed resources
//
// This method is only called when caching is enabled and a logger is available.
func (c *Client) showResourceDiff(oldResources, newResources []mcp.Resource) {
	// Create maps for easier comparison using resource URIs as keys
	oldMap := make(map[string]mcp.Resource)
	for _, resource := range oldResources {
		oldMap[resource.URI] = resource
	}

	newMap := make(map[string]mcp.Resource)
	for _, resource := range newResources {
		newMap[resource.URI] = resource
	}

	// Check for changes by comparing resource URI presence
	var added []string
	var removed []string
	var unchanged []string

	// Find added and unchanged resources
	for uri := range newMap {
		if _, exists := oldMap[uri]; exists {
			unchanged = append(unchanged, uri)
		} else {
			added = append(added, uri)
		}
	}

	// Find removed resources
	for uri := range oldMap {
		if _, exists := newMap[uri]; !exists {
			removed = append(removed, uri)
		}
	}

	// Display changes with appropriate visual indicators
	if len(added) > 0 || len(removed) > 0 {
		c.logger.Info("Resource changes detected:")
		for _, uri := range unchanged {
			c.logger.Success("  ✓ Unchanged: %s", uri)
		}
		for _, uri := range added {
			c.logger.Success("  + Added: %s", uri)
		}
		for _, uri := range removed {
			c.logger.Error("  - Removed: %s", uri)
		}
	} else {
		c.logger.Info("No resource changes detected")
	}
}

// showPromptDiff displays the differences between old and new prompt lists.
// This method is called when prompt change notifications are received to provide
// visual feedback about what prompts have been added or removed from the
// MCP server. It compares the prompt lists by name and categorizes changes as
// added, removed, or unchanged.
//
// The diff output uses color-coded logging:
//   - Green (✓) for unchanged prompts
//   - Green (+) for newly added prompts
//   - Red (-) for removed prompts
//
// This method is only called when caching is enabled and a logger is available.
func (c *Client) showPromptDiff(oldPrompts, newPrompts []mcp.Prompt) {
	// Create maps for easier comparison using prompt names as keys
	oldMap := make(map[string]mcp.Prompt)
	for _, prompt := range oldPrompts {
		oldMap[prompt.Name] = prompt
	}

	newMap := make(map[string]mcp.Prompt)
	for _, prompt := range newPrompts {
		newMap[prompt.Name] = prompt
	}

	// Check for changes by comparing prompt name presence
	var added []string
	var removed []string
	var unchanged []string

	// Find added and unchanged prompts
	for name := range newMap {
		if _, exists := oldMap[name]; exists {
			unchanged = append(unchanged, name)
		} else {
			added = append(added, name)
		}
	}

	// Find removed prompts
	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			removed = append(removed, name)
		}
	}

	// Display changes with appropriate visual indicators
	if len(added) > 0 || len(removed) > 0 {
		c.logger.Info("Prompt changes detected:")
		for _, name := range unchanged {
			c.logger.Success("  ✓ Unchanged: %s", name)
		}
		for _, name := range added {
			c.logger.Success("  + Added: %s", name)
		}
		for _, name := range removed {
			c.logger.Error("  - Removed: %s", name)
		}
	} else {
		c.logger.Info("No prompt changes detected")
	}
}
