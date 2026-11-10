package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// mcpServerKeySeparator separates the namespace from the name in the key of a
// server outside muster's namespace. Neither a namespace nor an object name
// can hold it.
const mcpServerKeySeparator = "/"

// ErrShadowedServer reports a server outside muster's namespace whose exposed
// names a server in muster's namespace can take (ShadowingServer).
var ErrShadowedServer = errors.New("an MCPServer outside muster's namespace may not share its exposed names with a server in muster's namespace")

// ErrIdentityOutsideNamespace reports a server outside muster's namespace that
// asks muster for an identity: the person's token, muster's own signature, a
// family or an audience in the login.
var ErrIdentityOutsideNamespace = errors.New("an MCPServer outside muster's namespace gets no identity from muster")

// MCPServerKey returns the key muster registers, exposes and stores a server
// under. A server in muster's namespace, or one without a namespace, keeps
// its bare name (in filesystem mode every server does: the caller passes no
// namespace there), so no tool name, toolset, workflow,
// skill or stored sign-in changes. A server outside muster's namespace always
// carries its namespace: "<namespace>/<name>".
func MCPServerKey(namespace, name, musterNamespace string) string {
	if namespace == "" || namespace == musterNamespace {
		return name
	}
	return namespace + mcpServerKeySeparator + name
}

// SplitMCPServerKey returns the namespace and the name of a key from
// MCPServerKey. The namespace is empty for a server of muster's namespace.
func SplitMCPServerKey(key string) (namespace, name string) {
	if ns, n, ok := strings.Cut(key, mcpServerKeySeparator); ok {
		return ns, n
	}
	return "", key
}

// MCPServerObject returns the namespace and the name of the MCPServer object
// a key names. musterNamespace is the namespace of a bare key.
func MCPServerObject(key, musterNamespace string) (namespace, name string) {
	namespace, name = SplitMCPServerKey(key)
	if namespace == "" {
		namespace = musterNamespace
	}
	return namespace, name
}

// ShadowingServer returns the first, by name, of the servers of muster's
// namespace (or without a namespace) among servers whose exposed names can
// equal those of a server in namespace. Every exposed name of a server in
// namespace starts with "<namespace>_" (MCPServerExposedPrefix), and a
// namespace name holds no '_', so a server of muster's namespace can take one
// only when its exposed prefix (its toolPrefix, else its name) or its family
// name is namespace or starts with "<namespace>_". The server of muster's
// namespace wins: the caller refuses the servers of namespace, whatever the
// order in which the servers appear.
func ShadowingServer(namespace, musterNamespace string, servers []MCPServerInfo) (string, bool) {
	if namespace == "" || namespace == musterNamespace {
		return "", false
	}
	var shadowing []string
	for _, server := range servers {
		if server.Namespace != "" && server.Namespace != musterNamespace {
			continue
		}
		prefix := server.ToolPrefix
		if prefix == "" {
			prefix = server.Name
		}
		heads := []string{prefix}
		if server.Family != nil && server.Family.Name != "" {
			heads = append(heads, server.Family.Name)
		}
		for _, head := range heads {
			if head == namespace || strings.HasPrefix(head, namespace+"_") {
				shadowing = append(shadowing, server.Name)
				break
			}
		}
	}
	if len(shadowing) == 0 {
		return "", false
	}
	sort.Strings(shadowing)
	return shadowing[0], true
}

// IsTenantMCPServerKey reports whether a key names a server outside muster's
// namespace.
func IsTenantMCPServerKey(key string) bool {
	return strings.Contains(key, mcpServerKeySeparator)
}

// MCPServerExposedPrefix returns the prefix of the exposed names of a server
// (without the muster prefix): its toolPrefix, or its name when it has none,
// and for a server outside muster's namespace its namespace in front of that,
// also when its name is unique. For example "org-a_github" for the server
// github in org-a, so its tool create_issue is x_org-a_github_create_issue.
func MCPServerExposedPrefix(key, toolPrefix string) string {
	namespace, name := SplitMCPServerKey(key)
	prefix := toolPrefix
	if prefix == "" {
		prefix = name
	}
	if namespace == "" {
		return prefix
	}
	return namespace + "_" + prefix
}

// ValidateServerNamespace reports a server that its namespace does not
// allow. Outside muster's namespace a server gets no identity from muster: requiredAudiences,
// forwardToken, forwardIdentity, tokenExchange, family and auth.type sigv4
// are refused, so it adds no audience to any login, receives no person's
// token except through its own sign-in, signs nothing as muster and joins no
// family.
func ValidateServerNamespace(namespace, musterNamespace string, family *MCPServerFamily, auth *MCPServerAuth) error {
	if namespace == "" || namespace == musterNamespace {
		return nil
	}
	var refused []string
	if family != nil {
		refused = append(refused, "family")
	}
	if auth != nil {
		if len(auth.RequiredAudiences) > 0 {
			refused = append(refused, "auth.requiredAudiences")
		}
		if auth.ForwardToken {
			refused = append(refused, "auth.forwardToken")
		}
		if auth.ForwardIdentity {
			refused = append(refused, "auth.forwardIdentity")
		}
		if auth.TokenExchange != nil {
			refused = append(refused, "auth.tokenExchange")
		}
		if auth.Type == MCPServerAuthTypeSigV4 {
			refused = append(refused, "auth.type sigv4")
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%w: %s %s not allowed in namespace %q; only a server in muster's namespace %q may use them",
			ErrIdentityOutsideNamespace, strings.Join(refused, ", "), isOrAre(len(refused)), namespace, musterNamespace)
	}
	return nil
}

func isOrAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// ValidateNotShadowed refuses a server of namespace when a server of muster's
// namespace among the served ones (list) can take its exposed names
// (ShadowingServer). It answers nil for a server of muster's namespace.
func ValidateNotShadowed(ctx context.Context, namespace, musterNamespace string, list func(context.Context) ([]MCPServerInfo, error)) error {
	if namespace == "" || namespace == musterNamespace || list == nil {
		return nil
	}
	servers, err := list(ctx)
	if err != nil {
		return fmt.Errorf("list the MCPServers of muster's namespace: %w", err)
	}
	if owner, ok := ShadowingServer(namespace, musterNamespace, servers); ok {
		return fmt.Errorf("%w: the server %q of muster's namespace %q exposes names under %q, which every server of namespace %q exposes its names under",
			ErrShadowedServer, owner, musterNamespace, namespace+"_", namespace)
	}
	return nil
}
