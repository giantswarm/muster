package api

import (
	"context"
	"maps"
	"strings"
)

// ToolCallMetadataExtension is kagent's MCP extension for request-scoped
// tool-call metadata. A client declares it with no settings; a server declares
// the metadata keys it accepts as {"keys": [...]}; a tools/call carries the
// values of those keys as the object _meta[ToolCallMetadataExtension], keyed
// by metadata key. The contract is kagent's
// docs/architecture/mcp-tool-call-metadata-extension.md.
const ToolCallMetadataExtension = "dev.kagent/tool-call-metadata"

type toolCallMetadataKey struct{}

// WithToolCallMetadata returns a context carrying the tool-call metadata
// values of the caller's tool call. Every backend call made under it,
// directly or from a workflow step, sends each backend the values of the keys
// that backend accepts.
func WithToolCallMetadata(ctx context.Context, values map[string]any) context.Context {
	return context.WithValue(ctx, toolCallMetadataKey{}, values)
}

// ToolCallMetadata returns the caller's tool-call metadata values of ctx.
func ToolCallMetadata(ctx context.Context) map[string]any {
	values, _ := ctx.Value(toolCallMetadataKey{}).(map[string]any)
	return values
}

// ToolCallMetadataValues returns the values of the tool-call metadata entry of
// a request's `_meta` whose key is a valid metadata key, or nil when the entry
// is missing, is not an object or holds no valid key.
func ToolCallMetadataValues(meta map[string]any) map[string]any {
	entry, ok := meta[ToolCallMetadataExtension].(map[string]any)
	if !ok {
		return nil
	}
	values := maps.Clone(entry)
	maps.DeleteFunc(values, func(key string, _ any) bool { return !IsToolCallMetadataKey(key) })
	if len(values) == 0 {
		return nil
	}
	return values
}

// IsToolCallMetadataKey reports whether key is a metadata key the extension
// carries: it follows MCP's `_meta` key grammar with a mandatory prefix that
// MCP does not reserve (second label modelcontextprotocol or mcp).
func IsToolCallMetadataKey(key string) bool {
	prefix, name, hasPrefix := strings.Cut(key, "/")
	if !hasPrefix || !validMetaName(name) {
		return false
	}
	labels := strings.Split(prefix, ".")
	for _, label := range labels {
		if !validMetaLabel(label) {
			return false
		}
	}
	return len(labels) < 2 || (labels[1] != "modelcontextprotocol" && labels[1] != "mcp")
}

func validMetaLabel(label string) bool {
	if label == "" || !isMetaLetter(label[0]) {
		return false
	}
	last := label[len(label)-1]
	if !isMetaLetter(last) && !isMetaDigit(last) {
		return false
	}
	for i := 1; i < len(label)-1; i++ {
		if c := label[i]; !isMetaLetter(c) && !isMetaDigit(c) && c != '-' {
			return false
		}
	}
	return true
}

func validMetaName(name string) bool {
	if name == "" || !isMetaAlnum(name[0]) || !isMetaAlnum(name[len(name)-1]) {
		return false
	}
	for i := 1; i < len(name)-1; i++ {
		if c := name[i]; !isMetaAlnum(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func isMetaLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isMetaDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isMetaAlnum(c byte) bool  { return isMetaLetter(c) || isMetaDigit(c) }
