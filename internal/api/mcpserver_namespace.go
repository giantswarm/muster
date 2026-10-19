package api

import (
	"errors"
	"fmt"
	"strings"
)

// TenantNamePrefix starts the name of every organization namespace. In
// muster's namespace a server name or toolPrefix may not start with it, so a
// server for everyone cannot take the exposed prefix of a tenant's servers.
const TenantNamePrefix = "org-"

// mcpServerKeySeparator separates the namespace from the name in the key of a
// server outside muster's namespace. Neither a namespace nor an object name
// can hold it.
const mcpServerKeySeparator = "/"

// ErrReservedServerName reports a server in muster's namespace whose name or
// toolPrefix starts with TenantNamePrefix.
var ErrReservedServerName = errors.New("an MCPServer in muster's namespace may not use the prefix of an organization namespace")

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

// ServedNamespace reports whether muster serves the MCPServer objects of a
// namespace when it serves other namespaces than its own: its own namespace
// (or none, in filesystem mode) and the organization namespaces, whose names
// start with TenantNamePrefix. A server name or toolPrefix in muster's
// namespace may not start with that prefix (ValidateServerNamespace), so the
// exposed prefix of a server for everyone ("<toolPrefix>") never equals the
// exposed prefix of an organization's server ("<namespace>_<toolPrefix>"),
// whatever the order in which the servers appear. A server in any other
// namespace is not served at all.
func ServedNamespace(namespace, musterNamespace string) bool {
	return namespace == "" || namespace == musterNamespace || strings.HasPrefix(namespace, TenantNamePrefix)
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
// allow. In muster's namespace (or without a namespace, in filesystem mode) a
// name or toolPrefix that starts with TenantNamePrefix is refused. Outside
// muster's namespace a server gets no identity from muster: requiredAudiences,
// forwardToken, forwardIdentity, tokenExchange, family and auth.type sigv4 are
// refused, so it adds no audience to any login, receives no person's token
// except through its own sign-in, signs nothing as muster and joins no family.
func ValidateServerNamespace(namespace, musterNamespace, name, toolPrefix string, family *MCPServerFamily, auth *MCPServerAuth) error {
	if namespace == "" || namespace == musterNamespace {
		if strings.HasPrefix(name, TenantNamePrefix) {
			return fmt.Errorf("%w: name %q starts with %q", ErrReservedServerName, name, TenantNamePrefix)
		}
		if strings.HasPrefix(toolPrefix, TenantNamePrefix) {
			return fmt.Errorf("%w: toolPrefix %q starts with %q", ErrReservedServerName, toolPrefix, TenantNamePrefix)
		}
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
