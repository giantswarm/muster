package api

import (
	"context"
	"maps"
	"strings"
)

type callerMetaKey struct{}

// WithCallerMeta carries the current tool call's negotiated extension data.
// Values are opaque and are never used as credentials.
func WithCallerMeta(ctx context.Context, entries, extensions map[string]any) context.Context {
	values := make(map[string]any)
	for identifier := range RelayedExtensions(extensions) {
		if value, present := entries[identifier]; present {
			values[identifier] = value
		}
	}
	return context.WithValue(ctx, callerMetaKey{}, values)
}

// CallerMeta returns the request-scoped extension metadata carried by ctx.
func CallerMeta(ctx context.Context) map[string]any {
	values, _ := ctx.Value(callerMetaKey{}).(map[string]any)
	return values
}

// RelayedExtensions keeps application extension declarations with valid
// identifiers and settings objects. MCP-owned extensions need their own
// routing behavior and are not advertised by this metadata relay.
func RelayedExtensions(extensions map[string]any) map[string]any {
	var relayed map[string]any
	for identifier, settings := range extensions {
		object, valid := settings.(map[string]any)
		if !valid || object == nil || !strings.Contains(identifier, "/") ||
			!requestMetaKeyPattern.MatchString(identifier) || reservedRequestMetaPrefix.MatchString(identifier) {
			continue
		}
		if relayed == nil {
			relayed = make(map[string]any)
		}
		relayed[identifier] = maps.Clone(object)
	}
	return relayed
}
