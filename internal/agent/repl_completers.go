package agent

import (
	"github.com/chzyer/readline"
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
		readline.PcItem("filter",
			readline.PcItem("tools"),
		),
		readline.PcItem("notifications",
			readline.PcItem("on"),
			readline.PcItem("off"),
		),
	)
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
