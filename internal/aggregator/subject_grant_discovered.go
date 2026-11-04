package aggregator

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// A server that discovers its authorization server through RFC 9728 and files
// its grants under the person (spec.auth.grantScope: subject, no
// spec.auth.authorizationServer.issuer) is subject-scoped for the issuer
// muster discovers for it. The decision lives in the MCPServer spec, which
// muster always has; what it applies to -- the issuer -- is learned from the
// server's resource metadata:
//
//   - in the background when the server registers and the 401 named no issuer
//     (lookupDiscoveredIssuer), never on the registration's own path;
//   - by a login (resolveServerAuthInfo), which is interactive and resolves
//     whatever the flow still lacks, as for every server.
//
// What was learned is remembered per server and URL (discoveredIssuers) for
// the life of the process: a re-registration whose 401 again names no issuer,
// and the server's definition while its registry entry is gone, still describe
// the issuer, so its pin is neither released nor looked up again. A failed
// lookup is remembered as well and not repeated before discoveredIssuerRetry;
// an adoption of the person's grant never waits for one.

// discoveredIssuerTimeout bounds one background resource-metadata lookup.
const discoveredIssuerTimeout = 10 * time.Second

// discoveredIssuerRetry is how long a failed lookup is not repeated.
const discoveredIssuerRetry = time.Minute

// discoveredIssuers remembers what muster learned about the authorization
// server of each server that discovers a subject-scoped issuer.
type discoveredIssuers struct {
	mu      sync.Mutex
	entries map[string]*discoveredIssuerEntry
}

type discoveredIssuerEntry struct {
	// url is the server URL the entry was learned for; another URL is
	// another server as far as discovery goes.
	url string
	// authInfo holds the issuer, scope and resource learned; zero while
	// nothing was.
	authInfo AuthInfo
	// failedAt is when the last lookup failed; zero after a success.
	failedAt time.Time
	// inFlight is set while a background lookup runs.
	inFlight bool
}

// entry returns the entry of a server for url, replacing one learned for
// another URL. The caller holds d.mu.
func (d *discoveredIssuers) entry(name, url string) *discoveredIssuerEntry {
	if d.entries == nil {
		d.entries = make(map[string]*discoveredIssuerEntry)
	}
	e := d.entries[name]
	if e == nil || e.url != url {
		e = &discoveredIssuerEntry{url: url}
		d.entries[name] = e
	}
	return e
}

// remembered returns a copy of what was learned for the server at url, or
// nil.
func (d *discoveredIssuers) remembered(name, url string) *AuthInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entries[name]
	if e == nil || e.url != url || e.authInfo.Issuer == "" {
		return nil
	}
	copied := e.authInfo
	return &copied
}

// remember records the issuer (with scope and resource) learned for the
// server at url. An AuthInfo without an issuer records nothing.
func (d *discoveredIssuers) remember(name, url string, authInfo *AuthInfo) {
	if authInfo == nil || authInfo.Issuer == "" || url == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entry(name, url)
	e.authInfo = AuthInfo{Issuer: authInfo.Issuer, Scope: authInfo.Scope, Resource: authInfo.Resource}
	e.failedAt = time.Time{}
}

// forget drops what was learned for a server whose definition went.
func (d *discoveredIssuers) forget(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.entries, name)
}

// beginLookup reports whether a lookup for the server at url should run now:
// none is running and none failed within discoveredIssuerRetry. It marks the
// lookup as running.
func (d *discoveredIssuers) beginLookup(name, url string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entry(name, url)
	if e.inFlight || (!e.failedAt.IsZero() && now.Sub(e.failedAt) < discoveredIssuerRetry) {
		return false
	}
	e.inFlight = true
	return true
}

// endLookup records the outcome of a lookup beginLookup started: what was
// learned, or a failure at now when authInfo holds no issuer.
func (d *discoveredIssuers) endLookup(name, url string, authInfo *AuthInfo, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entry(name, url)
	e.inFlight = false
	if authInfo == nil || authInfo.Issuer == "" {
		e.failedAt = now
		return
	}
	e.authInfo = AuthInfo{Issuer: authInfo.Issuer, Scope: authInfo.Scope, Resource: authInfo.Resource}
	e.failedAt = time.Time{}
}

// withRememberedIssuer returns the registration of a server that discovers a
// subject-scoped issuer with the issuer muster learned for it at the same
// URL, when the 401 named none; an issuer the 401 named is remembered. Any
// other registration is returned unchanged. It makes no request.
func (a *AggregatorServer) withRememberedIssuer(registration PendingAuthRegistration) PendingAuthRegistration {
	if !registration.AuthConfig.DiscoversSubjectScopedIssuer() || registration.URL == "" {
		return registration
	}
	if registration.AuthInfo != nil && registration.AuthInfo.Issuer != "" {
		a.discovered.remember(registration.Name, registration.URL, registration.AuthInfo)
		return registration
	}
	remembered := a.discovered.remembered(registration.Name, registration.URL)
	if remembered == nil {
		return registration
	}
	authInfo := &AuthInfo{}
	if registration.AuthInfo != nil {
		copied := *registration.AuthInfo
		authInfo = &copied
	}
	fillAuthInfo(authInfo, remembered.Issuer, remembered.Scope, remembered.Resource)
	registration.AuthInfo = authInfo
	return registration
}

// rememberedDefinition gives a definition of a server that discovers a
// subject-scoped issuer the issuer learned for it at the definition's URL, so
// the definition describes the issuer while the server's registry entry is
// gone (a failed connection) and the pin outlives the entry, as a pinned
// issuer's does.
func (a *AggregatorServer) rememberedDefinition(info *ServerInfo) {
	if info == nil || !info.AuthConfig.DiscoversSubjectScopedIssuer() {
		return
	}
	if remembered := a.discovered.remembered(info.Name, info.URL); remembered != nil {
		info.AuthInfo = remembered
	}
}

// lookupDiscoveredIssuer starts, in the background, the resource-metadata
// lookup of a registered server that discovers a subject-scoped issuer and
// whose entry knows none yet. On success the issuer goes on the entry (when it
// still has the URL) and its description is pinned. It returns at once; a
// lookup already running or one that failed within discoveredIssuerRetry is
// not repeated. The lookup ends with the aggregator.
func (a *AggregatorServer) lookupDiscoveredIssuer(name string) {
	info, ok := a.registry.GetServerInfo(name)
	if !ok || !info.AuthConfig.DiscoversSubjectScopedIssuer() || info.URL == "" {
		return
	}
	if authInfo := info.GetAuthInfo(); authInfo != nil && authInfo.Issuer != "" {
		return
	}
	url := info.URL
	if !a.discovered.beginLookup(name, url, time.Now()) {
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.refreshContext(), discoveredIssuerTimeout)
		defer cancel()
		a.runDiscoveredIssuerLookup(ctx, name, url)
	}()
}

// runDiscoveredIssuerLookup is the body of lookupDiscoveredIssuer.
func (a *AggregatorServer) runDiscoveredIssuerLookup(ctx context.Context, name, url string) {
	metadata, err := discoverProtectedResourceMetadata(ctx, url, nil)
	if err != nil || metadata.Issuer == "" {
		a.discovered.endLookup(name, url, nil, time.Now())
		reason := "the metadata names no authorization server"
		if err != nil {
			reason = err.Error()
		}
		logging.WarnWithAttrs("Aggregator", "Cannot discover the authorization server of a subject-scoped server; a login resolves it, or the next lookup after a minute",
			slog.String("server", name),
			slog.String("error", reason))
		return
	}
	learned := &AuthInfo{}
	fillFromResourceMetadata(learned, metadata)
	a.discovered.endLookup(name, url, learned, time.Now())
	logging.InfoWithAttrs("Aggregator", "Discovered the authorization server of a subject-scoped server",
		slog.String("server", name),
		slog.String("issuer", metadata.Issuer))

	current, ok := a.registry.GetServerInfo(name)
	if !ok || current.URL != url {
		return
	}
	authInfo := &AuthInfo{}
	if existing := current.GetAuthInfo(); existing != nil {
		copied := *existing
		authInfo = &copied
	}
	if fillFromResourceMetadata(authInfo, metadata) {
		current.SetAuthInfo(authInfo)
	}
	if err := a.pinDiscoveredIssuer(ctx, current); err != nil {
		logPinDeferred(current, err)
	}
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
			a.lookupDiscoveredIssuer(info.Name)
		}
		return "", "", nil
	}
	if err := a.pinDiscoveredIssuer(ctx, info); err != nil {
		return "", "", err
	}
	return issuer, scope, nil
}

// isDiscoveredDescription reports whether the server describes its
// authorization server only through the issuer it discovered
// (discoveredDescription): it pins no issuer of its own.
func isDiscoveredDescription(info *ServerInfo) bool {
	return info != nil && authorizationServerOf(info.AuthConfig) == nil && discoveredDescription(info) != nil
}

// effectiveSubjectScoped reports whether muster files the server's grants
// under the person: for a pinned issuer what the spec says; for a server that
// discovers its subject-scoped issuer, whether that issuer is known and its
// pin is subject-scoped. An operator's pin of the same issuer that is not
// leaves such a server session-scoped (issuerPinOwner).
func effectiveSubjectScoped(info *ServerInfo) bool {
	if info == nil || !info.AuthConfig.SubjectScoped() {
		return false
	}
	if !info.AuthConfig.DiscoversSubjectScopedIssuer() {
		return true
	}
	as := discoveredDescription(info)
	if as == nil {
		return false
	}
	return api.IssuerSubjectScoped(api.GetOAuthHandler(), api.ServerIssuerKey(info.Name, as.Issuer))
}

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
	issuer := api.ServerIssuerKey(info.Name, as.Issuer)
	if appOwnsIssuer(issuer) {
		return false
	}
	pinner, err := issuerPinner()
	if err != nil {
		return false
	}
	pinned, ok := pinner.PinnedDescriptionKey(issuer)
	return ok && pinned == discoveredPinKey()
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
// (discoveredDescription) when it is not the one pinned under the issuer key
// already, so a subject-scoped grant is found under it. It runs where muster
// learns or uses that issuer: the registration, a resolution of the server's
// auth info, a connection, an adoption of the person's grant and a logout.
// A discovered description never conflicts: under an issuer an operator's
// MCPServer names, the operator's description stands and nothing is pinned
// here (issuerPinOwner). Errors are those of pinning itself (OAuth not
// ready); nil for a server that pins its issuer or discovers none.
func (a *AggregatorServer) pinDiscoveredIssuer(ctx context.Context, info *ServerInfo) error {
	as := discoveredDescription(info)
	if as == nil {
		return nil
	}
	issuer := api.ServerIssuerKey(info.Name, as.Issuer)
	if pinner, err := issuerPinner(); err == nil {
		if pinned, ok := pinner.PinnedDescriptionKey(issuer); ok && pinned == discoveredPinKey() {
			return nil
		}
	}
	return a.pinAuthorizationServer(ctx, info)
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
	if as == nil {
		return
	}
	if pinned, _ := pinnedDescription(info); pinned != nil {
		return
	}
	issuer := api.ServerIssuerKey(info.Name, strings.TrimSuffix(as.Issuer, "/"))
	if issuer == "" || appOwnsIssuer(issuer) {
		return
	}
	pinner, err := issuerPinner()
	if err != nil {
		return
	}
	if pinned, ok := pinner.PinnedDescriptionKey(issuer); !ok || pinned != discoveredPinKey() {
		return
	}
	described := a.describedServers()
	described[info.Name] = info
	if owner := issuerOwner(described, issuer); owner != nil {
		return
	}
	pinner.UnpinIssuer(issuer)
	logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_unpinned",
		slog.String("server", info.Name),
		slog.String("issuer", issuer),
		slog.String("reason", "an operator names the issuer a server discovered"))
}
