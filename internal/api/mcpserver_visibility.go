package api

import (
	"context"
	"sync"
)

// ServerVisibility is what the caller of one request may see of the MCP
// servers: the servers of every namespace in which Kubernetes RBAC lets the
// caller list mcpservers.muster.giantswarm.io (agent-permissions D9). The zero
// value sees every server; that is the view when muster serves only its own
// namespace (no MCPServerVisibilityHandler is registered).
type ServerVisibility struct {
	restricted      bool
	musterNamespace string
	namespaces      map[string]bool

	// Reason says why the caller sees no server at all (a session without a
	// token, a token the API server does not accept), and that the person must
	// sign in again. Empty when the caller sees what RBAC allows.
	Reason string
}

// AllServersVisible is the view of a caller who sees every server.
func AllServersVisible() ServerVisibility {
	return ServerVisibility{}
}

// NoServerVisible is the view of a caller who sees no server, for reason.
func NoServerVisible(reason string) ServerVisibility {
	return ServerVisibility{restricted: true, Reason: reason}
}

// ServersOfNamespacesVisible is the view of a caller who may list the
// servers of the given namespaces. musterNamespace is the namespace of a
// server whose key carries none (MCPServerKey).
func ServersOfNamespacesVisible(musterNamespace string, namespaces []string) ServerVisibility {
	v := ServerVisibility{restricted: true, musterNamespace: musterNamespace, namespaces: make(map[string]bool, len(namespaces))}
	for _, namespace := range namespaces {
		v.namespaces[namespace] = true
	}
	return v
}

// Sees reports whether the caller may see the server with the given key.
func (v ServerVisibility) Sees(serverKey string) bool {
	if !v.restricted {
		return true
	}
	namespace, _ := MCPServerObject(serverKey, v.musterNamespace)
	return v.namespaces[namespace]
}

// SeesEvent reports whether the caller may see an event: an event about an
// MCPServer only when the caller sees the servers of its namespace.
func (v ServerVisibility) SeesEvent(event EventResult) bool {
	if !v.restricted || event.InvolvedObject.Kind != "MCPServer" {
		return true
	}
	namespace := event.InvolvedObject.Namespace
	if namespace == "" {
		namespace = event.Namespace
	}
	if namespace == "" {
		namespace = v.musterNamespace
	}
	return v.namespaces[namespace]
}

// Restricted reports whether the view hides any server.
func (v ServerVisibility) Restricted() bool {
	return v.restricted
}

// MCPServerVisibilityHandler answers which MCP servers the caller of a
// request may see. It is registered only when muster serves MCPServer objects
// of every namespace (mcpServersInAllNamespaces); without it every caller
// sees every server, as before.
type MCPServerVisibilityHandler interface {
	// CallerVisibility returns the view of the caller whose ID token ctx
	// carries. It fails closed: no token, or a token the API server does not
	// accept, sees no server.
	CallerVisibility(ctx context.Context) ServerVisibility

	// LoginAudience is the audience muster's own login must ask for, so the
	// API server accepts the session's ID token.
	LoginAudience() string
}

var (
	visibilityMu      sync.RWMutex
	visibilityHandler MCPServerVisibilityHandler
)

// RegisterMCPServerVisibility registers the visibility handler; nil removes it.
func RegisterMCPServerVisibility(h MCPServerVisibilityHandler) {
	visibilityMu.Lock()
	defer visibilityMu.Unlock()
	visibilityHandler = h
}

// GetMCPServerVisibility returns the registered visibility handler, or nil.
func GetMCPServerVisibility() MCPServerVisibilityHandler {
	visibilityMu.RLock()
	defer visibilityMu.RUnlock()
	return visibilityHandler
}

// CallerVisibility returns the view of the caller of ctx: every server when
// no visibility handler is registered.
func CallerVisibility(ctx context.Context) ServerVisibility {
	h := GetMCPServerVisibility()
	if h == nil {
		return AllServersVisible()
	}
	return h.CallerVisibility(ctx)
}
