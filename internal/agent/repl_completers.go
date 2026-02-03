package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/mark3labs/mcp-go/mcp"
)

// NoSpaceDynamicCompleter is a custom completer that doesn't add trailing spaces
// for completions ending with special characters like "=".
// This is needed because readline's built-in PcItemDynamic always adds a trailing space.
type NoSpaceDynamicCompleter struct {
	Callback func(string) []string
	Children []readline.PrefixCompleterInterface
}

// GetName returns an empty name since this is a dynamic completer
func (n *NoSpaceDynamicCompleter) GetName() []rune {
	return nil
}

// GetChildren returns the child completers
func (n *NoSpaceDynamicCompleter) GetChildren() []readline.PrefixCompleterInterface {
	return n.Children
}

// SetChildren sets the child completers
func (n *NoSpaceDynamicCompleter) SetChildren(children []readline.PrefixCompleterInterface) {
	n.Children = children
}

// IsDynamic returns true since this is a dynamic completer
func (n *NoSpaceDynamicCompleter) IsDynamic() bool {
	return true
}

// GetDynamicNames returns completions WITHOUT trailing spaces for items ending with "="
func (n *NoSpaceDynamicCompleter) GetDynamicNames(line []rune) [][]rune {
	var names [][]rune
	for _, name := range n.Callback(string(line)) {
		// Don't add trailing space for completions ending with "="
		// This allows users to immediately type the value
		if strings.HasSuffix(name, "=") {
			names = append(names, []rune(name))
		} else {
			names = append(names, []rune(name+" "))
		}
	}
	return names
}

// Print implements the PrefixCompleterInterface
func (n *NoSpaceDynamicCompleter) Print(prefix string, level int, buf *bytes.Buffer) {
	// Dynamic completers don't print static names
}

// Do implements the AutoCompleter interface
func (n *NoSpaceDynamicCompleter) Do(line []rune, pos int) ([][]rune, int) {
	return doNoSpaceInternal(n, line, pos, line)
}

// doNoSpaceInternal handles the completion logic
func doNoSpaceInternal(p readline.PrefixCompleterInterface, line []rune, pos int, origLine []rune) ([][]rune, int) {
	// Trim leading spaces
	trimmed := line[:pos]
	for len(trimmed) > 0 && trimmed[0] == ' ' {
		trimmed = trimmed[1:]
	}

	var newLine [][]rune
	var offset int
	var lineCompleter readline.PrefixCompleterInterface
	goNext := false

	for _, child := range p.GetChildren() {
		var childNames [][]rune

		if dynChild, ok := child.(interface {
			IsDynamic() bool
			GetDynamicNames([]rune) [][]rune
		}); ok && dynChild.IsDynamic() {
			childNames = dynChild.GetDynamicNames(origLine)
		} else {
			childNames = [][]rune{child.GetName()}
		}

		for _, childName := range childNames {
			if len(trimmed) >= len(childName) {
				if hasPrefix(trimmed, childName) {
					if len(trimmed) == len(childName) {
						newLine = append(newLine, []rune{' '})
					} else {
						newLine = append(newLine, childName)
					}
					offset = len(childName)
					lineCompleter = child
					goNext = true
				}
			} else {
				if hasPrefix(childName, trimmed) {
					newLine = append(newLine, childName[len(trimmed):])
					offset = len(trimmed)
					lineCompleter = child
				}
			}
		}
	}

	if len(newLine) != 1 {
		return newLine, offset
	}

	tmpLine := make([]rune, 0, len(trimmed))
	for i := offset; i < len(trimmed); i++ {
		if trimmed[i] == ' ' {
			continue
		}
		tmpLine = append(tmpLine, trimmed[i:]...)
		return doNoSpaceInternal(lineCompleter, tmpLine, len(tmpLine), origLine)
	}

	if goNext {
		return doNoSpaceInternal(lineCompleter, nil, 0, origLine)
	}
	return newLine, offset
}

// hasPrefix checks if s starts with prefix
func hasPrefix(s, prefix []rune) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

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
		// PcItem for the tool name, with NoSpaceDynamicCompleter for parameter completion
		// NoSpaceDynamicCompleter doesn't add trailing space for "param=" completions
		toolCompleter[i] = readline.PcItem(tool.Name,
			&NoSpaceDynamicCompleter{Callback: r.createToolParamCompleter(tool)})
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
		// PcItem for the prompt name, with NoSpaceDynamicCompleter for argument completion
		// NoSpaceDynamicCompleter doesn't add trailing space for "arg=" completions
		promptCompleter[i] = readline.PcItem(prompt.Name,
			&NoSpaceDynamicCompleter{Callback: r.createPromptArgCompleter(prompt)})
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

// filterInput filters input characters for readline
func filterInput(r rune) (rune, bool) {
	switch r {
	// block CtrlZ feature
	case readline.CharCtrlZ:
		return r, false
	}
	return r, true
}
