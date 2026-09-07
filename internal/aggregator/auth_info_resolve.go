package aggregator

import (
	"context"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// GetAuthInfo returns the server's OAuth information, or nil.
func (s *ServerInfo) GetAuthInfo() *AuthInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AuthInfo
}

// SetAuthInfo records the server's OAuth information on the registry entry.
func (s *ServerInfo) SetAuthInfo(info *AuthInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AuthInfo = info
}

// oauthProtected reports whether tool calls on the server run on a per-session
// OAuth connection: the MCPServer declares auth type oauth, or the server
// answered muster's probe with a 401 (a 401-time AuthInfo exists). Servers
// that forward or exchange the caller's token are decided before this.
func oauthProtected(s *ServerInfo) bool {
	if s.GetAuthInfo() != nil {
		return true
	}
	return s.AuthConfig != nil && strings.EqualFold(s.AuthConfig.Type, "oauth")
}

// resolveServerAuthInfo returns the server's OAuth information with the
// issuer (and scope, resource) filled in from its RFC 9728 metadata or its
// pinned authorization server (spec.auth.authorizationServer), and records the
// result on the registry entry so every later caller finds it.
//
// The 401 that registers a server as pending auth carries only the
// resource-metadata pointer, not the issuer. Until now only a full
// core_auth_login filled the issuer in -- by mutating the entry's AuthInfo as
// a side effect -- and a session whose authenticated flag outlived a muster
// restart never runs one (its login short-circuits with "already
// authenticated"), so its tool calls could not choose an auth method.
//
// A pin that cannot be applied is an error (the login must not proceed with
// a client the operator did not intend); a discovery failure is logged and
// leaves the caller with whatever the entry already knew.
func (a *AggregatorServer) resolveServerAuthInfo(ctx context.Context, serverInfo *ServerInfo) (*AuthInfo, error) {
	authInfo := &AuthInfo{}
	if current := serverInfo.GetAuthInfo(); current != nil {
		copied := *current
		authInfo = &copied
	}
	changed := applyAuthorizationServerPin(authInfo, serverInfo.AuthConfig)
	if !needsResourceMetadata(authInfo, serverInfo.URL) {
		if changed {
			serverInfo.SetAuthInfo(authInfo)
		}
		return authInfo, nil
	}

	if err := pinAuthorizationServer(ctx, serverInfo); err != nil {
		return nil, err
	}
	var override *api.MCPServerAuthAuthorizationServer
	if serverInfo.AuthConfig != nil {
		override = serverInfo.AuthConfig.AuthorizationServer
	}
	metadata, err := discoverProtectedResourceMetadata(ctx, serverInfo.URL, override)
	if err != nil {
		logging.Warn("AuthTools", "Failed to discover protected resource metadata for %s: %v", serverInfo.Name, err)
		return authInfo, nil
	}

	if authInfo.Issuer == "" && metadata.Issuer != "" {
		authInfo.Issuer = metadata.Issuer
		changed = true
		logging.Info("AuthTools", "Discovered authorization server for %s: %s", serverInfo.Name, metadata.Issuer)
	}
	if authInfo.Scope == "" && metadata.Scope != "" {
		authInfo.Scope = metadata.Scope
		changed = true
		logging.Info("AuthTools", "Discovered required scope for %s: %s", serverInfo.Name, metadata.Scope)
	}
	if authInfo.Resource == "" && metadata.Resource != "" {
		authInfo.Resource = metadata.Resource
		changed = true
	}
	if changed {
		serverInfo.SetAuthInfo(authInfo)
	}
	return authInfo, nil
}

// applyAuthorizationServerPin makes an operator's pin
// (spec.auth.authorizationServer) the authority on a server's authorization
// server: its issuer -- and its scopes, when it names any -- replace what the
// 401 probe learned from the endpoint's own RFC 9728 metadata. The issuer is
// the key everything files or looks the session's grant up under (the
// challenge, the token store, the connection, the logout), so there must be
// exactly one, and the operator's declaration wins over an endpoint that
// advertises a different authorization server (giantswarm/muster#1174).
// Reports whether authInfo changed.
func applyAuthorizationServerPin(authInfo *AuthInfo, auth *api.MCPServerAuth) bool {
	as := authorizationServerOf(auth)
	if as == nil || authInfo == nil {
		return false
	}
	changed := false
	if issuer := strings.TrimSuffix(as.Issuer, "/"); authInfo.Issuer != issuer {
		authInfo.Issuer = issuer
		changed = true
	}
	if as.Scopes != "" && authInfo.Scope != as.Scopes {
		authInfo.Scope = as.Scopes
		changed = true
	}
	return changed
}
