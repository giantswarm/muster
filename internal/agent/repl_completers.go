package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chzyer/readline"
	"github.com/mark3labs/mcp-go/mcp"
)

// createCompleter creates the tab completion configuration using the command registry
func (r *REPL) createCompleter() *readline.PrefixCompleter {
	// Get lists for completion
	r.client.mu.RLock()
	tools := make([]string, len(r.client.toolCache))
	for i, tool := range r.client.toolCache {
		tools[i] = tool.Name
	}

	resources := make([]string, len(r.client.resourceCache))
	for i, resource := range r.client.resourceCache {
		resources[i] = resource.URI
	}

	prompts := make([]string, len(r.client.promptCache))
	for i, prompt := range r.client.promptCache {
		prompts[i] = prompt.Name
	}
	r.client.mu.RUnlock()

	// Get workflow names dynamically
	workflows := r.getWorkflowNames()

	// Create dynamic completers for items
	toolCompleter := make([]readline.PrefixCompleterInterface, len(tools))
	for i, tool := range tools {
		toolCompleter[i] = readline.PcItem(tool)
	}

	resourceCompleter := make([]readline.PrefixCompleterInterface, len(resources))
	for i, resource := range resources {
		resourceCompleter[i] = readline.PcItem(resource)
	}

	promptCompleter := make([]readline.PrefixCompleterInterface, len(prompts))
	for i, prompt := range prompts {
		promptCompleter[i] = readline.PcItem(prompt)
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

// filterInput filters input characters for readline
func filterInput(r rune) (rune, bool) {
	switch r {
	// block CtrlZ feature
	case readline.CharCtrlZ:
		return r, false
	}
	return r, true
}
