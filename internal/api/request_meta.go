package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// AWSRegionRequestMetaKey is the `_meta` key the AWS-hosted MCP server reads
// the region it operates in from. Without it the server runs every call in
// us-east-1, whatever region the endpoint is in.
const AWSRegionRequestMetaKey = "AWS_REGION"

var (
	// requestMetaKeyPattern is the MCP `_meta` key format: an optional prefix
	// of dot-separated labels ending in '/', then a name that begins and ends
	// with a letter or digit.
	requestMetaKeyPattern = regexp.MustCompile(`^([a-zA-Z]([a-zA-Z0-9-]*[a-zA-Z0-9])?([.][a-zA-Z]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*/)?[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

	// reservedRequestMetaPrefix matches a prefix whose second label is
	// modelcontextprotocol or mcp, which MCP reserves for itself.
	reservedRequestMetaPrefix = regexp.MustCompile(`^(?i)[^./]+[.](modelcontextprotocol|mcp)([.][^/]*)?/`)
)

// reservedRequestMetaKeys are set per request by muster (progressToken) or by
// OpenTelemetry context propagation, so a fixed value would overwrite theirs.
var reservedRequestMetaKeys = map[string]bool{
	"progressToken": true,
	"traceparent":   true,
	"tracestate":    true,
	"baggage":       true,
}

// ResolveRequestMeta returns requestMeta, or meta when only the deprecated
// field is set. Setting both is refused rather than merged, so a definition
// never carries two sources for the same entry.
func ResolveRequestMeta(requestMeta, meta map[string]string) (map[string]string, error) {
	if len(requestMeta) > 0 && len(meta) > 0 {
		return nil, errors.New("meta is the deprecated name of requestMeta: set requestMeta only")
	}
	if len(requestMeta) > 0 {
		return requestMeta, nil
	}
	return meta, nil
}

// ValidateRequestMeta reports whether requestMeta is usable with the given
// server type and auth.
//
// The entries are added by an HTTP round tripper, so a stdio server would
// accept the map and drop it. A dropped or missing entry fails nowhere: the
// AWS-hosted backend falls back to us-east-1 and answers about the wrong
// region. That silence is why both cases are refused here rather than left to
// the connect attempt.
//
// The CRD states the same rules in CEL, which only runs in Kubernetes mode.
// Keep the two in step, as ValidateSigV4 and ValidateStdioAllowed do.
func ValidateRequestMeta(serverType string, auth *MCPServerAuth, requestMeta map[string]string) error {
	// Keyed on the sigv4 block, as the CRD's rule is: a sigv4 type without the
	// block is ValidateSigV4's error to report.
	if auth != nil && auth.Type == MCPServerAuthTypeSigV4 && auth.SigV4 != nil && requestMeta[AWSRegionRequestMetaKey] == "" {
		return fmt.Errorf("a %s server needs requestMeta.%s: the AWS MCP server runs every call in us-east-1 without it",
			MCPServerAuthTypeSigV4, AWSRegionRequestMetaKey)
	}
	if len(requestMeta) == 0 {
		return nil
	}
	if serverType == string(MCPServerTypeStdio) {
		return fmt.Errorf("requestMeta is only allowed when type is %q or %q, not %q: the entries are added to params._meta by an HTTP transport, which a stdio server does not have",
			MCPServerTypeStreamableHTTP, MCPServerTypeSSE, serverType)
	}

	keys := make([]string, 0, len(requestMeta))
	for key := range requestMeta {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var problems []string
	for _, key := range keys {
		switch {
		case !requestMetaKeyPattern.MatchString(key):
			problems = append(problems, fmt.Sprintf("%q does not follow the MCP _meta key format", key))
		case reservedRequestMetaPrefix.MatchString(key):
			problems = append(problems, fmt.Sprintf("%q uses a prefix reserved for MCP", key))
		case reservedRequestMetaKeys[key]:
			problems = append(problems, fmt.Sprintf("%q is set by muster or OpenTelemetry, not by configuration", key))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid requestMeta: %s", strings.Join(problems, "; "))
	}
	return nil
}
