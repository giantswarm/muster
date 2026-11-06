package aggregator

import (
	"maps"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
)

// How a server describes its authorization server, and how two descriptions
// compare: the pieces the pins (authorization_server_pin.go) are built from.

// pinnedDescription returns the description a server pins and the issuer key
// it is pinned under; nil when the server pins nothing. A plain issuer
// override, without a client, endpoints or subject-scoped grants, pins
// nothing; nor does a server that discovers its authorization server, unless
// its grants are subject-scoped and muster knows the issuer it discovered.
func pinnedDescription(info *ServerInfo) (*api.MCPServerAuthAuthorizationServer, string) {
	as := serverDescription(info)
	if !pinsSomething(as) {
		return nil, ""
	}
	return as, namedIssuerKey(info.Name, as)
}

// pinsSomething reports whether a description pins anything: a client,
// endpoints or subject-scoped grants.
func pinsSomething(as *api.MCPServerAuthAuthorizationServer) bool {
	return as != nil && (as.ClientCredentialsSecretRef != nil || as.HasPinnedEndpoints() || as.SubjectScoped())
}

// namedIssuerKey is the issuer key of the issuer a description names, without
// a trailing slash (api.IssuerKey trims only for a tenant key).
func namedIssuerKey(serverName string, as *api.MCPServerAuthAuthorizationServer) string {
	if as == nil {
		return ""
	}
	return api.ServerIssuerKey(serverName, strings.TrimSuffix(as.Issuer, "/"))
}

// serverDescription returns how a server describes its authorization server:
// its pin (authorizationServerOf), else the description of the issuer it
// discovered (discoveredDescription); nil for neither.
func serverDescription(info *ServerInfo) *api.MCPServerAuthAuthorizationServer {
	if info == nil {
		return nil
	}
	if as := authorizationServerOf(info.AuthConfig); as != nil {
		return as
	}
	return discoveredDescription(info)
}

// authorizationServerOf returns the authorization server an MCPServer's auth
// config describes (spec.auth.authorizationServer with an issuer), or nil.
// spec.auth.grantScope: subject applies to the pinned issuer as well, so the
// description returned carries it.
func authorizationServerOf(auth *api.MCPServerAuth) *api.MCPServerAuthAuthorizationServer {
	if auth == nil || auth.AuthorizationServer == nil || auth.AuthorizationServer.Issuer == "" {
		return nil
	}
	if auth.SubjectScoped() && !auth.AuthorizationServer.SubjectScoped() {
		as := *auth.AuthorizationServer
		as.GrantScope = api.GrantScopeSubject
		return &as
	}
	return auth.AuthorizationServer
}

// descriptionKey identifies what a description pins: endpoints (revocation
// included), expected issuer, client Secret reference and grant scope. A
// rotated Secret behind an unchanged reference keeps the key; the re-pin
// every login performs picks that up.
func descriptionKey(as *api.MCPServerAuthAuthorizationServer) string {
	fields := []string{as.AuthorizationEndpoint, as.TokenEndpoint, as.ExpectedIssuer, as.RevocationEndpoint,
		strconv.FormatBool(as.SubjectScoped())}
	if ref := as.ClientCredentialsSecretRef; ref != nil {
		fields = append(fields, ref.Name, ref.Namespace, ref.ClientIDKey, ref.ClientSecretKey)
	}
	return strings.Join(fields, "\x00")
}

// samePin reports whether two descriptions pin the same (descriptionKey).
func samePin(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return descriptionKey(a) == descriptionKey(b)
}

// sameAuthorizationServer reports whether two descriptions of an authorization
// server are the same: issuer, scopes and what they pin (samePin).
func sameAuthorizationServer(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSuffix(a.Issuer, "/") == strings.TrimSuffix(b.Issuer, "/") &&
		a.Scopes == b.Scopes &&
		samePin(a, b)
}

// describedServers returns every server that describes its authorization
// server to muster: the MCPServer definitions, connected or not, overlaid by
// the registry's entries. A definition whose backend is down still describes
// its authorization server, so a deregistration on a failed connection keeps
// the pin.
func (a *AggregatorServer) describedServers() map[string]*ServerInfo {
	described := make(map[string]*ServerInfo)
	if a.definedServers != nil {
		maps.Copy(described, a.definedServers())
	}
	maps.Copy(described, a.registry.GetAllServers())
	return described
}
