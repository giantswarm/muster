package aggregator

import (
	"context"
	"log/slog"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// A server that discovers its authorization server through RFC 9728 and files
// its grants under the person (spec.auth.grantScope: subject, no
// spec.auth.authorizationServer.issuer) is subject-scoped for the issuer
// muster discovers for it. The decision lives in the MCPServer spec, which
// muster always has; what it applies to -- the issuer -- is learned from the
// server's resource metadata (subject_grant_discovered_lookup.go). This file
// holds how such a server describes that issuer, and what it may take under
// it: an issuer that has an issuer key, the person's grant under a discovered
// description only, and a grant bound to the server's own resource.

// discoveredDescription returns the description a server with
// spec.auth.grantScope: subject and no pinned issuer gives the issuer muster
// discovered for it (the registry entry's AuthInfo): that issuer, its grants
// filed under the person, discovery and client identification as without a
// pin. Nil for any other server, while the issuer is not known yet, and for
// an issuer that has no issuer key (api.ServerIssuerKey): the backend's
// metadata named "/" or a value that reads as another namespace's key.
func discoveredDescription(info *ServerInfo) *api.MCPServerAuthAuthorizationServer {
	if info == nil || !info.AuthConfig.DiscoversSubjectScopedIssuer() {
		return nil
	}
	authInfo := info.GetAuthInfo()
	if authInfo == nil || authInfo.Issuer == "" {
		return nil
	}
	issuer := strings.TrimSuffix(authInfo.Issuer, "/")
	if api.ServerIssuerKey(info.Name, issuer) == "" {
		return nil
	}
	return &api.MCPServerAuthAuthorizationServer{
		Issuer:     issuer,
		GrantScope: api.GrantScopeSubject,
	}
}

// discoveredPinKey is the IssuerPin.DescriptionKey of a discovered
// description. It is set apart from descriptionKey's, so an operator's pin
// that says only grantScope: subject is told from it: muster adopts the
// person's grant for a discovered server only under its own kind of pin
// (discoveredPinStands).
func discoveredPinKey() string {
	return descriptionKey(&api.MCPServerAuthAuthorizationServer{GrantScope: api.GrantScopeSubject}) + "\x00discovered"
}

// pinnedDescriptionKey is the IssuerPin.DescriptionKey a server's description
// is pinned with: descriptionKey, and for a discovered description
// discoveredPinKey, which an operator's pin that says the same never has.
func pinnedDescriptionKey(info *ServerInfo, as *api.MCPServerAuthAuthorizationServer) string {
	if isDiscoveredDescription(info) {
		return discoveredPinKey()
	}
	return descriptionKey(as)
}

// isDiscoveredDescription reports whether the server describes its
// authorization server only through the issuer it discovered
// (discoveredDescription): it pins no issuer of its own.
func isDiscoveredDescription(info *ServerInfo) bool {
	return discoveredDescription(info) != nil
}

// effectiveSubjectScoped reports whether muster files the server's grants
// under the person: for a pinned issuer what the spec says; for a server that
// discovers its subject-scoped issuer, whether that issuer is known and its
// pin is subject-scoped. An operator's pin of the same issuer that is not
// leaves such a server session-scoped (issuerPinOwner). A server that says
// subject but neither pins nor discovers an issuer is session-scoped.
func effectiveSubjectScoped(info *ServerInfo) bool {
	if info == nil || !info.AuthConfig.SubjectScoped() {
		return false
	}
	if !info.AuthConfig.DiscoversSubjectScopedIssuer() {
		return authorizationServerOf(info.AuthConfig) != nil
	}
	as := discoveredDescription(info)
	if as == nil {
		return false
	}
	return api.IssuerSubjectScoped(api.GetOAuthHandler(), namedIssuerKey(info.Name, as))
}

// discoveredPinStands reports whether the pin under the issuer key of a
// server that discovers its subject-scoped issuer is a discovered
// description, so the person's grant under the key was filed by servers that
// discovered the issuer too. False while the issuer is unknown, and when a
// provider App or an operator's MCPServer describes the issuer: the grants
// under the key are then theirs, whatever scope their pin gives them, and a
// backend that merely names the issuer in its metadata never receives them.
func discoveredPinStands(info *ServerInfo) bool {
	as := discoveredDescription(info)
	if as == nil {
		return false
	}
	issuer := namedIssuerKey(info.Name, as)
	return !appOwnsIssuer(issuer) && pinnedUnder(issuer, discoveredPinKey())
}

// grantBoundToServer reports whether a grant found under a server's issuer
// key was issued for that server. Servers that discover their subject-scoped
// issuer share the key with every other server whose backend names the same
// authorization server, and such an authorization server binds each token to
// the RFC 8707 resource it was requested for. So for such a server the grant
// counts only when it records the server's own resource, on the server's own
// origin; a grant recorded without one is not taken. A grant of a pinned
// issuer is the server's whatever it records, as before. declared is the
// resource the server's metadata declared, from which the sign-in's resource
// is formed (resourceIndicator).
func grantBoundToServer(info *ServerInfo, declared string, token *api.OAuthToken) bool {
	if info == nil || token == nil {
		return false
	}
	if !info.AuthConfig.DiscoversSubjectScopedIssuer() {
		return true
	}
	resolved, err := pkgoauth.ResolveResourceIndicator(tenantResource(info.Name, declared, info.URL), info.URL)
	if err != nil || resolved.Value == "" || token.Resource != resolved.Value {
		return false
	}
	return pkgoauth.ValidateAdvertisedResource(token.Resource, info.URL) == nil
}

// pinDiscoveredIssuer pins the description of the issuer a server discovered
// (discoveredDescription) when nothing stands under the issuer key yet, so a
// subject-scoped grant is found under it. It runs where muster learns or uses
// that issuer: the registration, a resolution of the server's auth info, a
// connection, an adoption of the person's grant and a logout, so it answers
// without looking at every other MCPServer whenever the key is decided: its
// own pin stands, or another description's (an operator's or a provider
// App's) -- a discovered description never takes a key from one, so there is
// nothing to do. Only a key without a pin is decided by the owner
// (issuerPinOwner): an operator that names the issuer without pinning it
// keeps it unpinned. Errors are those of pinning itself (OAuth not ready);
// nil for a server that pins its issuer or discovers none.
func (a *AggregatorServer) pinDiscoveredIssuer(ctx context.Context, info *ServerInfo) error {
	as := discoveredDescription(info)
	if as == nil {
		return nil
	}
	issuer := namedIssuerKey(info.Name, as)
	if appOwnsIssuer(issuer) {
		return nil
	}
	if pinner, err := issuerPinner(); err == nil {
		if _, ok := pinner.PinnedDescriptionKey(issuer); ok {
			return nil
		}
	}
	return a.pinOwnerDescription(ctx, info)
}

// withdrawDiscoveredPin unpins the discovered description under the issuer
// key a server names in spec.auth.authorizationServer without pinning
// anything, when no operator's description owns the key: the issuer an
// operator named is not subject-scoped because a backend's metadata names it.
func (a *AggregatorServer) withdrawDiscoveredPin(info *ServerInfo) {
	if info == nil {
		return
	}
	as := authorizationServerOf(info.AuthConfig)
	if as == nil || pinsSomething(as) {
		return
	}
	issuer := namedIssuerKey(info.Name, as)
	if issuer == "" || appOwnsIssuer(issuer) || !pinnedUnder(issuer, discoveredPinKey()) {
		return
	}
	described := a.describedServers()
	described[info.Name] = info
	if owner := issuerOwner(described, issuer); owner != nil {
		return
	}
	unpinDiscovered(issuer, "an operator names the issuer a server discovered")
}

// unpinDiscovered unpins a discovered description standing under an issuer
// key, and leaves any other pin alone.
func unpinDiscovered(issuer, reason string) {
	if issuer == "" || !pinnedUnder(issuer, discoveredPinKey()) {
		return
	}
	pinner, err := issuerPinner()
	if err != nil {
		return
	}
	pinner.UnpinIssuer(issuer)
	logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_unpinned",
		slog.String("issuer", issuer),
		slog.String("reason", reason))
}

// subjectGrantIssuer returns the issuer key and scope a session finds the
// person's grant for the server under (sessionOAuthIssuer), and makes sure
// the description of a discovered issuer is pinned (pinDiscoveredIssuer). For
// a server that discovers its subject-scoped issuer and whose entry knows
// none yet it starts the background lookup and returns no issuer: an
// adoption never waits for the network; the person's next call or a login
// finds the issuer.
func (a *AggregatorServer) subjectGrantIssuer(ctx context.Context, info *ServerInfo) (issuer, scope string, err error) {
	issuer, scope = sessionOAuthIssuer(info)
	if issuer == "" {
		if info.AuthConfig.DiscoversSubjectScopedIssuer() {
			a.lookupDiscoveredIssuer(info.Name, false)
		}
		return "", "", nil
	}
	if err := a.pinDiscoveredIssuer(ctx, info); err != nil {
		return "", "", err
	}
	return issuer, scope, nil
}

// notifyAllSubjectsCapabilitiesChanged tells every live session that its
// tool, resource and prompt lists may have changed, so a client that cached
// its lists lists again and adopts the person's grant.
func (a *AggregatorServer) notifyAllSubjectsCapabilitiesChanged() {
	if a.subjectSessions == nil {
		return
	}
	methods := capabilityMethodsFor(true, true, true)
	for _, sub := range a.subjectSessions.Subjects() {
		a.notifySubjectMethods(sub, methods)
	}
}

// reconcileDiscoveredIssuer runs on the definitions' reconcile tick for a
// definition of a server that discovers a subject-scoped issuer: a known
// issuer gets its pin (a replica that never learned it pins it), an unknown
// one of a registered server is looked up, within discoveredIssuerRetry.
func (a *AggregatorServer) reconcileDiscoveredIssuer(ctx context.Context, definition *ServerInfo) {
	target := definition
	registered, ok := a.registry.GetServerInfo(definition.Name)
	if ok {
		target = registered
	}
	if discoveredDescription(target) != nil {
		if err := a.pinDiscoveredIssuer(ctx, target); err != nil {
			logPinDeferred(target, err)
		}
		return
	}
	if ok {
		a.lookupDiscoveredIssuer(definition.Name, false)
	}
}
