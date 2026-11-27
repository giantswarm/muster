package api

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type callerMetaKey struct{}

var callerMetaKeyPattern = regexp.MustCompile(`^([A-Za-z]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z]([A-Za-z0-9-]*[A-Za-z0-9])?)*/)?([A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?)?$`)

// WithCallerMeta carries the current tool call's application metadata.
// Values are opaque and are never used as credentials.
func WithCallerMeta(ctx context.Context, entries map[string]any) (context.Context, error) {
	for key := range entries {
		if !callerMetaKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("%w: invalid MCP metadata key %q", mcp.ErrInvalidParams, key)
		}
	}
	values := maps.Clone(entries)
	maps.DeleteFunc(values, func(key string, _ any) bool {
		if reservedRequestMetaKeys[key] {
			return true
		}
		switch key {
		case mcp.MetaKeyProtocolVersion, mcp.MetaKeyClientInfo, mcp.MetaKeyClientCapabilities,
			mcp.MetaKeyLogLevel, mcp.MetaKeyServerInfo, mcp.MetaKeySubscriptionID:
			return true
		}
		return false
	})
	return context.WithValue(ctx, callerMetaKey{}, values), nil
}

// CallerMeta returns the request-scoped application metadata carried by ctx.
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
