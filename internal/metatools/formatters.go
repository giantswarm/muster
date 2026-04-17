package metatools

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
)

// installationPlaceholder is the literal substituted in place of an
// installation identifier in a deduplicated tool name.
const installationPlaceholder = "<installation>"

// Formatters provides utilities for formatting MCP data consistently.
// It supports structured JSON responses for tools, resources, and prompts.
// The formatters ensure consistent presentation across different output modes.
//
// Key features:
//   - JSON formatting for structured data consumption
//   - Search and lookup utilities for cached data
//   - Consistent error handling and fallback formatting
type Formatters struct{}

// NewFormatters creates a new formatters instance.
// The formatters instance is stateless and can be safely used concurrently.
func NewFormatters() *Formatters {
	return &Formatters{}
}

// FormatToolsListJSON formats a list of tools as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - tools: Slice of tools to format
//
// Returns:
//   - JSON string containing array of tool objects with name and description
//   - error: JSON marshaling errors (should be rare)
//
// Output format:
//
//	[
//	  {
//	    "name": "tool_name",
//	    "description": "Tool description"
//	  }
//	]
//
// If no tools are available, returns a simple message string.
func (f *Formatters) FormatToolsListJSON(tools []mcp.Tool) (string, error) {
	if len(tools) == 0 {
		return "No tools available", nil
	}

	type ToolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	toolList := make([]ToolInfo, len(tools))
	for i, tool := range tools {
		toolList[i] = ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
		}
	}

	jsonData, err := json.MarshalIndent(toolList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format tools: %w", err)
	}

	return string(jsonData), nil
}

// FormatToolsListWithAuthJSON formats a list of tools along with information
// about servers requiring authentication as structured JSON. Tools that share
// an (originalName, description) signature across multiple backend servers are
// collapsed into a single entry whose name uses an <installation> placeholder
// and whose `installations` field enumerates the distinct installation values
// extracted from the member servers.
//
// The `resolve` callback maps a fully prefixed tool name back to its origin
// (serverName, originalName). Tools for which resolve returns ok == false —
// for example core_* tools that never pass through the aggregator registry —
// are always emitted as concrete standalone entries.
//
// Output format:
//
//	{
//	  "tools": [
//	    {"name": "core_auth_login", "description": "..."},
//	    {"name": "x_<installation>-mcp-prometheus_execute_query", "description": "...",
//	     "installations": ["enigma", "galaxy", ...]}
//	  ],
//	  "servers_requiring_auth": [...]
//	}
func (f *Formatters) FormatToolsListWithAuthJSON(
	tools []mcp.Tool,
	serversRequiringAuth []api.ServerAuthInfo,
	resolve func(exposedName string) (serverName, originalName string, ok bool),
) (string, error) {
	type ToolInfo struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		Installations []string `json:"installations,omitempty"`
	}

	type Response struct {
		Tools                []ToolInfo           `json:"tools"`
		ServersRequiringAuth []api.ServerAuthInfo `json:"servers_requiring_auth,omitempty"`
	}

	// signature groups together exposed tools sharing an (originalName, description)
	// pair from resolvable backend servers.
	type signature struct {
		originalName string
		description  string
	}
	type groupMember struct {
		exposedName string
		serverName  string
	}

	var standalone []ToolInfo
	groups := make(map[signature][]groupMember)

	for _, tool := range tools {
		serverName, originalName, ok := noopResolve(resolve)(tool.Name)
		if !ok {
			// Unresolvable (core_* or unknown) — emit as-is.
			standalone = append(standalone, ToolInfo{
				Name:        tool.Name,
				Description: tool.Description,
			})
			continue
		}
		sig := signature{originalName: originalName, description: tool.Description}
		groups[sig] = append(groups[sig], groupMember{exposedName: tool.Name, serverName: serverName})
	}

	// Stable iteration order for deterministic output.
	sigKeys := make([]signature, 0, len(groups))
	for k := range groups {
		sigKeys = append(sigKeys, k)
	}
	sort.Slice(sigKeys, func(i, j int) bool {
		if sigKeys[i].originalName != sigKeys[j].originalName {
			return sigKeys[i].originalName < sigKeys[j].originalName
		}
		return sigKeys[i].description < sigKeys[j].description
	})

	toolList := append([]ToolInfo(nil), standalone...)
	for _, sig := range sigKeys {
		members := groups[sig]
		if len(members) == 1 {
			toolList = append(toolList, ToolInfo{
				Name:        members[0].exposedName,
				Description: sig.description,
			})
			continue
		}

		names := make([]string, len(members))
		for i, m := range members {
			names[i] = m.exposedName
		}
		pattern, installations, ok := buildInstallationPattern(names)
		if !ok {
			// No usable common affix across members — fall back to emitting
			// each member as its own standalone entry.
			for _, m := range members {
				toolList = append(toolList, ToolInfo{
					Name:        m.exposedName,
					Description: sig.description,
				})
			}
			continue
		}

		toolList = append(toolList, ToolInfo{
			Name:          pattern,
			Description:   sig.description,
			Installations: installations,
		})
	}

	sort.SliceStable(toolList, func(i, j int) bool {
		return toolList[i].Name < toolList[j].Name
	})

	response := Response{
		Tools:                toolList,
		ServersRequiringAuth: serversRequiringAuth,
	}

	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format tools: %w", err)
	}

	return string(jsonData), nil
}

// noopResolve wraps a nullable resolve callback, returning a callback that
// treats every input as unresolvable when the underlying resolver is nil.
func noopResolve(resolve func(string) (string, string, bool)) func(string) (string, string, bool) {
	if resolve == nil {
		return func(string) (string, string, bool) { return "", "", false }
	}
	return resolve
}

// buildInstallationPattern computes the <installation>-placeholder pattern and
// the extracted installation identifiers for a group of exposed tool names
// that share the same (originalName, description) signature. It returns
// ok == false when no meaningful word-boundary-aligned common affix exists,
// signalling the caller to fall back to listing members individually.
func buildInstallationPattern(names []string) (pattern string, installations []string, ok bool) {
	prefix := trimPrefixToBoundary(longestCommonPrefix(names))
	suffix := trimSuffixToBoundary(longestCommonSuffix(names))

	if prefix == "" && suffix == "" {
		return "", nil, false
	}

	installations = make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if len(prefix)+len(suffix) > len(n) {
			return "", nil, false
		}
		inst := n[len(prefix) : len(n)-len(suffix)]
		if inst == "" {
			return "", nil, false
		}
		if !seen[inst] {
			seen[inst] = true
			installations = append(installations, inst)
		}
	}

	sort.Strings(installations)
	return prefix + installationPlaceholder + suffix, installations, true
}

func longestCommonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	prefix := names[0]
	for _, n := range names[1:] {
		maxLen := len(prefix)
		if len(n) < maxLen {
			maxLen = len(n)
		}
		i := 0
		for i < maxLen && prefix[i] == n[i] {
			i++
		}
		prefix = prefix[:i]
		if prefix == "" {
			break
		}
	}
	return prefix
}

func longestCommonSuffix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	suffix := names[0]
	for _, n := range names[1:] {
		maxLen := len(suffix)
		if len(n) < maxLen {
			maxLen = len(n)
		}
		i := 0
		for i < maxLen && suffix[len(suffix)-1-i] == n[len(n)-1-i] {
			i++
		}
		suffix = suffix[len(suffix)-i:]
		if suffix == "" {
			break
		}
	}
	return suffix
}

// trimPrefixToBoundary shortens p until it ends on a `_` or `-` boundary, or
// returns an empty string if no such boundary exists. This prevents a shared
// initial character (e.g. all installations starting with `a`) from being
// consumed into the pattern prefix.
func trimPrefixToBoundary(p string) string {
	for len(p) > 0 {
		last := p[len(p)-1]
		if last == '_' || last == '-' {
			return p
		}
		p = p[:len(p)-1]
	}
	return ""
}

// trimSuffixToBoundary shortens s forward until it starts on a `_` or `-`
// boundary, or returns an empty string.
func trimSuffixToBoundary(s string) string {
	for len(s) > 0 {
		first := s[0]
		if first == '_' || first == '-' {
			return s
		}
		s = s[1:]
	}
	return ""
}

// FormatResourcesListJSON formats a list of resources as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - resources: Slice of resources to format
//
// Returns:
//   - JSON string containing array of resource objects with URI, name, description, and MIME type
//   - error: JSON marshaling errors (should be rare)
//
// If no resources are available, returns a simple message string.
func (f *Formatters) FormatResourcesListJSON(resources []mcp.Resource) (string, error) {
	if len(resources) == 0 {
		return "No resources available", nil
	}

	type ResourceInfo struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		MIMEType    string `json:"mimeType,omitempty"`
	}

	resourceList := make([]ResourceInfo, len(resources))
	for i, resource := range resources {
		desc := resource.Description
		if desc == "" {
			desc = resource.Name
		}
		resourceList[i] = ResourceInfo{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: desc,
			MIMEType:    resource.MIMEType,
		}
	}

	jsonData, err := json.MarshalIndent(resourceList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format resources: %w", err)
	}

	return string(jsonData), nil
}

// FormatPromptsListJSON formats a list of prompts as structured JSON.
// This format is used for programmatic consumption, MCP server responses,
// and integration with external tools that expect structured data.
//
// Args:
//   - prompts: Slice of prompts to format
//
// Returns:
//   - JSON string containing array of prompt objects with name and description
//   - error: JSON marshaling errors (should be rare)
//
// If no prompts are available, returns a simple message string.
func (f *Formatters) FormatPromptsListJSON(prompts []mcp.Prompt) (string, error) {
	if len(prompts) == 0 {
		return "No prompts available", nil
	}

	type PromptInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	promptList := make([]PromptInfo, len(prompts))
	for i, prompt := range prompts {
		promptList[i] = PromptInfo{
			Name:        prompt.Name,
			Description: prompt.Description,
		}
	}

	jsonData, err := json.MarshalIndent(promptList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format prompts: %w", err)
	}

	return string(jsonData), nil
}

// FormatToolDetailJSON formats detailed tool information as structured JSON.
// This format includes the complete tool schema and is used for programmatic
// consumption and tool introspection.
//
// Args:
//   - tool: The tool to format detailed information for
//
// Returns:
//   - JSON string containing complete tool information including schema
//   - error: JSON marshaling errors (should be rare)
//
// Output format:
//
//	{
//	  "name": "tool_name",
//	  "description": "Tool description",
//	  "inputSchema": { ... }
//	}
func (f *Formatters) FormatToolDetailJSON(tool mcp.Tool) (string, error) {
	toolInfo := map[string]interface{}{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
	}

	jsonData, err := json.MarshalIndent(toolInfo, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format tool info: %w", err)
	}

	return string(jsonData), nil
}

// FormatResourceDetailJSON formats detailed resource information as structured JSON.
// This format includes all available resource metadata and is used for programmatic
// consumption and resource introspection.
//
// Args:
//   - resource: The resource to format detailed information for
//
// Returns:
//   - JSON string containing complete resource information
//   - error: JSON marshaling errors (should be rare)
func (f *Formatters) FormatResourceDetailJSON(resource mcp.Resource) (string, error) {
	resourceInfo := map[string]interface{}{
		"uri":         resource.URI,
		"name":        resource.Name,
		"description": resource.Description,
		"mimeType":    resource.MIMEType,
	}

	jsonData, err := json.MarshalIndent(resourceInfo, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format resource info: %w", err)
	}

	return string(jsonData), nil
}

// FormatPromptDetailJSON formats detailed prompt information as structured JSON.
// This format includes argument specifications and is used for programmatic
// consumption and prompt introspection.
//
// Args:
//   - prompt: The prompt to format detailed information for
//
// Returns:
//   - JSON string containing complete prompt information including arguments
//   - error: JSON marshaling errors (should be rare)
func (f *Formatters) FormatPromptDetailJSON(prompt mcp.Prompt) (string, error) {
	promptInfo := map[string]interface{}{
		"name":        prompt.Name,
		"description": prompt.Description,
	}

	if len(prompt.Arguments) > 0 {
		args := make([]map[string]interface{}, len(prompt.Arguments))
		for i, arg := range prompt.Arguments {
			args[i] = map[string]interface{}{
				"name":        arg.Name,
				"description": arg.Description,
				"required":    arg.Required,
			}
		}
		promptInfo["arguments"] = args
	}

	jsonData, err := json.MarshalIndent(promptInfo, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format prompt info: %w", err)
	}

	return string(jsonData), nil
}

// FindTool searches for a tool by name in the provided tool list.
// This is a utility method for command implementations and internal lookups.
//
// Args:
//   - tools: Slice of tools to search in
//   - name: Exact name of the tool to find
//
// Returns:
//   - Pointer to the found tool, or nil if not found
//
// The search is case-sensitive and requires exact name matching.
func (f *Formatters) FindTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// FindResource searches for a resource by URI in the provided resource list.
// This is a utility method for command implementations and internal lookups.
//
// Args:
//   - resources: Slice of resources to search in
//   - uri: Exact URI of the resource to find
//
// Returns:
//   - Pointer to the found resource, or nil if not found
//
// The search is case-sensitive and requires exact URI matching.
func (f *Formatters) FindResource(resources []mcp.Resource, uri string) *mcp.Resource {
	for _, resource := range resources {
		if resource.URI == uri {
			return &resource
		}
	}
	return nil
}

// FindPrompt searches for a prompt by name in the provided prompt list.
// This is a utility method for command implementations and internal lookups.
//
// Args:
//   - prompts: Slice of prompts to search in
//   - name: Exact name of the prompt to find
//
// Returns:
//   - Pointer to the found prompt, or nil if not found
//
// The search is case-sensitive and requires exact name matching.
func (f *Formatters) FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return &prompt
		}
	}
	return nil
}

// SerializeContent serializes MCP content items to a format suitable for JSON.
// This preserves the full structure of content items for proper response unwrapping.
//
// Args:
//   - content: Slice of MCP content interfaces
//
// Returns:
//   - Slice of serializable content representations
func SerializeContent(content []mcp.Content) []interface{} {
	result := make([]interface{}, 0, len(content))
	for _, item := range content {
		if textContent, ok := mcp.AsTextContent(item); ok {
			result = append(result, map[string]interface{}{
				"type": "text",
				"text": textContent.Text,
			})
		} else if imageContent, ok := mcp.AsImageContent(item); ok {
			result = append(result, map[string]interface{}{
				"type":     "image",
				"mimeType": imageContent.MIMEType,
				"dataSize": len(imageContent.Data),
			})
		} else if audioContent, ok := mcp.AsAudioContent(item); ok {
			result = append(result, map[string]interface{}{
				"type":     "audio",
				"mimeType": audioContent.MIMEType,
				"dataSize": len(audioContent.Data),
			})
		} else {
			// Fallback for unknown content types
			result = append(result, item)
		}
	}
	return result
}
