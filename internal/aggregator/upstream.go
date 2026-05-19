package aggregator

import "strings"

// proxyURLFor returns the dial URL muster's aggregator uses to reach the
// named MCPServer via agentgateway: <proxy>/mcp/<name>. Trailing slashes on
// the proxy are stripped so the join is single-slash regardless of caller
// hygiene. Callers MUST validate proxy non-empty before invoking; an empty
// proxy yields a relative path and is a misconfiguration.
func proxyURLFor(proxy, name string) string {
	return strings.TrimRight(proxy, "/") + "/mcp/" + name
}
