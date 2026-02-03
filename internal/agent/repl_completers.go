package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/mark3labs/mcp-go/mcp"
)

// createCompleter creates the tab completion configuration using the command registry
func (r *REPL) createCompleter() *readline.PrefixCompleter {
	// Get lists for completion
	r.client.mu.RLock()
	// Copy tool cache for dynamic completers
	toolCache := make([]mcp.Tool, len(r.client.toolCache))
	copy(toolCache, r.client.toolCache)

	resources := make([]string, len(r.client.resourceCache))
	for i, resource := range r.client.resourceCache {
		resources[i] = resource.URI
	}

	// Copy prompt cache for dynamic completers
	promptCache := make([]mcp.Prompt, len(r.client.promptCache))
	copy(promptCache, r.client.promptCache)
	r.client.mu.RUnlock()

	// Get workflow names dynamically
	workflows := r.getWorkflowNames()

	// Create dynamic completers for tools - each tool gets its own dynamic completer for parameters
	toolCompleter := make([]readline.PrefixCompleterInterface, len(toolCache))
	for i := range toolCache {
		// Capture tool for closure by taking address of slice element
		tool := &toolCache[i]
		// PcItem for the tool name, with PcItemDynamic as a child for parameter completion
		// The nested PcItemDynamic(noTrailingSpace) prevents readline from adding a trailing space
		// after "param=" completions, allowing the user to immediately type the value
		toolCompleter[i] = readline.PcItem(tool.Name,
			readline.PcItemDynamic(r.createToolParamCompleter(tool),
				readline.PcItemDynamic(noTrailingSpace)))
	}

	resourceCompleter := make([]readline.PrefixCompleterInterface, len(resources))
	for i, resource := range resources {
		resourceCompleter[i] = readline.PcItem(resource)
	}

	// Create dynamic completers for prompts - each prompt gets its own dynamic completer for arguments
	promptCompleter := make([]readline.PrefixCompleterInterface, len(promptCache))
	for i := range promptCache {
		// Capture prompt for closure by taking address of slice element
		prompt := &promptCache[i]
		// PcItem for the prompt name, with PcItemDynamic as a child for argument completion
		// The nested PcItemDynamic(noTrailingSpace) prevents readline from adding a trailing space
		promptCompleter[i] = readline.PcItem(prompt.Name,
			readline.PcItemDynamic(r.createPromptArgCompleter(prompt),
				readline.PcItemDynamic(noTrailingSpace)))
	}

	workflowCompleter := make([]readline.PrefixCompleterInterface, len(workflows))
	for i, workflow := range workflows {
		workflowCompleter[i] = readline.PcItem(workflow)
	}

	// Get all command names from registry
	commandNames := r.commandRegistry.AllCompletions()
	commandCompleters := make([]readline.PrefixCompleterInterface, len(commandNames))
	for i, name := range commandNames {
		commandCompleters[i] = readline.PcItem(name)
	}

	return readline.NewPrefixCompleter(
		// Commands with their specific completions
		readline.PcItem("help", commandCompleters...),
		readline.PcItem("?"),
		readline.PcItem("exit"),
		readline.PcItem("quit"),
		readline.PcItem("list",
			readline.PcItem("tools"),
			readline.PcItem("resources"),
			readline.PcItem("prompts"),
			readline.PcItem("workflows"),
			readline.PcItem("core-tools"),
		),
		readline.PcItem("describe",
			readline.PcItem("tool", toolCompleter...),
			readline.PcItem("resource", resourceCompleter...),
			readline.PcItem("prompt", promptCompleter...),
		),
		readline.PcItem("call", toolCompleter...),
		readline.PcItem("get", resourceCompleter...),
		readline.PcItem("prompt", promptCompleter...),
		readline.PcItem("workflow", workflowCompleter...),
		readline.PcItem("wf", workflowCompleter...),
		readline.PcItem("run-workflow", workflowCompleter...),
		readline.PcItem("filter",
			readline.PcItem("tools"),
		),
		readline.PcItem("notifications",
			readline.PcItem("on"),
			readline.PcItem("off"),
		),
	)
}

// getWorkflowNames fetches workflow names for tab completion
func (r *REPL) getWorkflowNames() []string {
	// Create a context with a short timeout for completion queries
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Call the core_workflow_list tool to get workflows
	result, err := r.client.CallTool(ctx, "core_workflow_list", map[string]interface{}{})
	if err != nil {
		return []string{}
	}

	if result.IsError {
		return []string{}
	}

	// Parse workflow names from result (same logic as in workflow command)
	var names []string

	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			var jsonResult interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonResult); err == nil {
				if resultMap, ok := jsonResult.(map[string]interface{}); ok {
					if workflowsData, exists := resultMap["workflows"]; exists {
						if workflows, ok := workflowsData.([]interface{}); ok {
							for _, workflow := range workflows {
								if workflowMap, ok := workflow.(map[string]interface{}); ok {
									if name, exists := workflowMap["name"]; exists {
										if nameStr, ok := name.(string); ok {
											names = append(names, nameStr)
										}
									}
								}
							}
						}
					}
				}
			}
			break // Only process first text content
		}
	}

	return names
}

// createToolParamCompleter returns a dynamic completion function for a specific tool's parameters
func (r *REPL) createToolParamCompleter(tool *mcp.Tool) readline.DynamicCompleteFunc {
	return func(line string) []string {
		if tool == nil || len(tool.InputSchema.Properties) == 0 {
			return []string{}
		}

		// Get parameter names
		var params []string
		for name := range tool.InputSchema.Properties {
			params = append(params, name)
		}
		sort.Strings(params)

		// Filter out parameters that have already been specified
		var completions []string
		for _, param := range params {
			// Check if this param is already in the line
			if !strings.Contains(line, param+"=") {
				completions = append(completions, param+"=")
			}
		}

		return completions
	}
}

// createPromptArgCompleter returns a dynamic completion function for a specific prompt's arguments
func (r *REPL) createPromptArgCompleter(prompt *mcp.Prompt) readline.DynamicCompleteFunc {
	return func(line string) []string {
		if prompt == nil || len(prompt.Arguments) == 0 {
			return []string{}
		}

		// Get argument names
		var args []string
		for _, arg := range prompt.Arguments {
			args = append(args, arg.Name)
		}
		sort.Strings(args)

		// Filter out arguments that have already been specified
		var completions []string
		for _, arg := range args {
			// Check if this arg is already in the line
			if !strings.Contains(line, arg+"=") {
				completions = append(completions, arg+"=")
			}
		}

		return completions
	}
}

// noTrailingSpace is a dynamic completion function that returns empty completions.
// When used as a child of a parameter completer, it signals to readline that more input
// is expected, preventing the addition of a trailing space after "param=" completions.
func noTrailingSpace(_ string) []string {
	return []string{}
}

// filterInput filters input characters for readline
func filterInput(r rune) (rune, bool) {
	switch r {
	// block CtrlZ feature
	case readline.CharCtrlZ:
		return r, false
	}
	return r, true
}
