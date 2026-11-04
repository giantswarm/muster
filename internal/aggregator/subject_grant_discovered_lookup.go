package aggregator

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The issuer a server that discovers its subject-scoped authorization server
// applies its grant scope to is learned from the server's resource metadata:
//
//   - in the background when the server registers (lookupDiscoveredIssuer),
//     never on the registration's own path: when the 401 named no issuer, and
//     again when the registration got the issuer from memory, so a backend
//     that moved to another authorization server at the same URL is followed;
//   - by the definitions' reconcile tick, for a registered server whose
//     issuer is still unknown, so every replica converges;
//   - by a login (resolveServerAuthInfo), which is interactive and resolves
//     whatever the flow still lacks, as for every server.
//
// What was learned is remembered per server and URL (discoveredIssuers)
// until the MCPServer is deleted or its URL changes: a re-registration whose
// 401 again names no issuer, and the server's definition while its registry
// entry is gone, still describe the issuer, so its pin is not released. A
// failed lookup is remembered as well and not repeated before
// discoveredIssuerRetry; an adoption of the person's grant never waits for
// one. The issuer is recorded without a trailing slash, as a pinned issuer is,
// so the person's grant is filed and found under the key its pin has.

// discoveredIssuerTimeout bounds one background resource-metadata lookup.
const discoveredIssuerTimeout = 10 * time.Second

// discoveredIssuerRetry is how long a failed lookup is not repeated.
const discoveredIssuerRetry = time.Minute

// discoveredIssuers remembers what muster learned about the authorization
// server of each server that discovers a subject-scoped issuer.
type discoveredIssuers struct {
	mu      sync.Mutex
	entries map[string]*discoveredIssuerEntry
	// lookups numbers the lookups begun, so one that ends after its entry
	// was replaced or forgotten changes nothing.
	lookups uint64
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
	// lookup is the number of the background lookup running, zero when none
	// is.
	lookup uint64
}

// learn records what was learned, the issuer without a trailing slash.
func (e *discoveredIssuerEntry) learn(authInfo *AuthInfo) {
	e.authInfo = AuthInfo{Issuer: strings.TrimSuffix(authInfo.Issuer, "/"), Scope: authInfo.Scope, Resource: authInfo.Resource}
	e.failedAt = time.Time{}
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
	d.entry(name, url).learn(authInfo)
}

// forget drops what was learned for a server whose definition went, and
// returns the issuer it held ("" for none).
func (d *discoveredIssuers) forget(name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entries[name]
	delete(d.entries, name)
	if e == nil {
		return ""
	}
	return e.authInfo.Issuer
}

// beginLookup reports whether a lookup for the server at url should run now:
// none is running and none failed within discoveredIssuerRetry. It marks the
// lookup as running and returns its number for endLookup.
func (d *discoveredIssuers) beginLookup(name, url string, now time.Time) (uint64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entry(name, url)
	if e.lookup != 0 || (!e.failedAt.IsZero() && now.Sub(e.failedAt) < discoveredIssuerRetry) {
		return 0, false
	}
	d.lookups++
	e.lookup = d.lookups
	return e.lookup, true
}

// endLookup records the outcome of the lookup beginLookup numbered: what was
// learned, or a failure at now when authInfo holds no issuer. It changes
// nothing, and reports false, when the server's entry was forgotten or
// replaced (another URL) since the lookup began: the entry is never
// re-created for a server that went.
func (d *discoveredIssuers) endLookup(name, url string, lookup uint64, authInfo *AuthInfo, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.entries[name]
	if e == nil || e.url != url || e.lookup != lookup {
		return false
	}
	e.lookup = 0
	if authInfo == nil || authInfo.Issuer == "" {
		e.failedAt = now
		return true
	}
	e.learn(authInfo)
	return true
}

// cancelLookup clears the running mark of a lookup that never ran.
func (d *discoveredIssuers) cancelLookup(name, url string, lookup uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := d.entries[name]; e != nil && e.url == url && e.lookup == lookup {
		e.lookup = 0
	}
}

// copyAuthInfo returns a copy of authInfo, or an empty AuthInfo for nil.
func copyAuthInfo(authInfo *AuthInfo) *AuthInfo {
	if authInfo == nil {
		return &AuthInfo{}
	}
	copied := *authInfo
	return &copied
}

// withRememberedAuthInfo is authInfo (copied) with the issuer, scope and
// resource remembered for the server filled in where it lacks them.
func withRememberedAuthInfo(authInfo, remembered *AuthInfo) *AuthInfo {
	filled := copyAuthInfo(authInfo)
	fillAuthInfo(filled, remembered.Issuer, remembered.Scope, remembered.Resource)
	return filled
}

// withRememberedIssuer returns the registration of a server that discovers a
// subject-scoped issuer with the issuer muster learned for it at the same
// URL, when the 401 named none, and reports that it did: the caller looks the
// issuer up again in the background (lookupDiscoveredIssuer) in case the
// backend moved. An issuer the 401 named is remembered, without a trailing
// slash. Any other registration is returned unchanged. It makes no request.
func (a *AggregatorServer) withRememberedIssuer(registration PendingAuthRegistration) (PendingAuthRegistration, bool) {
	if !registration.AuthConfig.DiscoversSubjectScopedIssuer() || registration.URL == "" {
		return registration, false
	}
	if registration.AuthInfo != nil && registration.AuthInfo.Issuer != "" {
		named := copyAuthInfo(registration.AuthInfo)
		named.Issuer = strings.TrimSuffix(named.Issuer, "/")
		a.discovered.remember(registration.Name, registration.URL, named)
		registration.AuthInfo = named
		return registration, false
	}
	remembered := a.discovered.remembered(registration.Name, registration.URL)
	if remembered == nil {
		return registration, false
	}
	registration.AuthInfo = withRememberedAuthInfo(registration.AuthInfo, remembered)
	return registration, true
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
		info.AuthInfo = withRememberedAuthInfo(info.AuthInfo, remembered)
	}
}

// lookupDiscoveredIssuer starts, in the background, the resource-metadata
// lookup of a registered server that discovers a subject-scoped issuer: when
// its entry knows no issuer yet, or, with revalidate, when the entry's issuer
// came from memory. On success the issuer goes on the entry (when it still
// has the URL; a different issuer replaces the remembered one) and its
// description is pinned. It returns at once; a lookup already running or one
// that failed within discoveredIssuerRetry is not repeated, and none starts
// once the aggregator stops.
func (a *AggregatorServer) lookupDiscoveredIssuer(name string, revalidate bool) {
	info, ok := a.registry.GetServerInfo(name)
	if !ok || !info.AuthConfig.DiscoversSubjectScopedIssuer() || info.URL == "" {
		return
	}
	if authInfo := info.GetAuthInfo(); !revalidate && authInfo != nil && authInfo.Issuer != "" {
		return
	}
	url := info.URL
	lookup, ok := a.discovered.beginLookup(name, url, clock.Now())
	if !ok {
		return
	}
	started := a.goBackground(func() {
		ctx, cancel := context.WithTimeout(a.refreshContext(), discoveredIssuerTimeout)
		defer cancel()
		a.runDiscoveredIssuerLookup(ctx, name, url, lookup)
	})
	if !started {
		a.discovered.cancelLookup(name, url, lookup)
	}
}

// runDiscoveredIssuerLookup is the body of lookupDiscoveredIssuer.
func (a *AggregatorServer) runDiscoveredIssuerLookup(ctx context.Context, name, url string, lookup uint64) {
	metadata, err := discoverProtectedResourceMetadata(ctx, url, nil)
	if err != nil || metadata.Issuer == "" {
		a.discovered.endLookup(name, url, lookup, nil, clock.Now())
		reason := "the metadata names no authorization server"
		if err != nil {
			reason = err.Error()
		}
		logging.WarnWithAttrs("Aggregator", "Cannot discover the authorization server of a subject-scoped server; a login resolves it, or the next lookup after a minute",
			slog.String("server", name),
			slog.String("error", reason))
		return
	}
	if !a.discovered.endLookup(name, url, lookup, &AuthInfo{Issuer: metadata.Issuer, Scope: metadata.Scope, Resource: metadata.Resource}, clock.Now()) {
		// The MCPServer was deleted, or moved to another URL, meanwhile.
		return
	}
	logging.InfoWithAttrs("Aggregator", "Discovered the authorization server of a subject-scoped server",
		slog.String("server", name),
		slog.String("issuer", metadata.Issuer))

	current, ok := a.registry.GetServerInfo(name)
	if !ok || current.URL != url {
		return
	}
	hadIssuer := discoveredDescription(current) != nil
	if _, err := a.recordResourceMetadata(ctx, current, copyAuthInfo(current.GetAuthInfo()), false, metadata); err != nil {
		logPinDeferred(current, err)
		return
	}
	if !hadIssuer && discoveredPinStands(current) {
		// A session that listed while the issuer was unknown has no tools of
		// this server; its next listing adopts the person's grant.
		a.notifyAllSubjectsCapabilitiesChanged()
	}
}

// recordResourceMetadata records what a server's resource metadata names on
// authInfo (the server's AuthInfo or a copy the caller already filled, with
// changed saying whether it did) and on the registry entry: the issuer,
// scope and resource it lacks. For a server that discovers its subject-scoped
// issuer it also normalizes the issuer, replaces a remembered issuer the
// metadata no longer names (the backend moved to another authorization
// server), remembers what was learned, records the issuer key with a tenant
// server's owner record, releases the description of an issuer it no longer
// names, and pins the discovered description. Errors are those of the pin.
func (a *AggregatorServer) recordResourceMetadata(ctx context.Context, info *ServerInfo, authInfo *AuthInfo, changed bool, metadata *ProtectedResourceMetadata) (*AuthInfo, error) {
	discovered := info.AuthConfig.DiscoversSubjectScopedIssuer()
	previous := discoveredDescription(info)
	if discovered && metadata != nil && metadata.Issuer != "" && authInfo.Issuer != "" &&
		strings.TrimSuffix(metadata.Issuer, "/") != strings.TrimSuffix(authInfo.Issuer, "/") {
		logging.InfoWithAttrs("Aggregator", "The authorization server of a subject-scoped server moved",
			slog.String("server", info.Name),
			slog.String("previousIssuer", authInfo.Issuer),
			slog.String("issuer", metadata.Issuer))
		authInfo.Issuer, authInfo.Scope, authInfo.Resource = "", "", ""
		changed = true
	}

	hadIssuer, hadScope := authInfo.Issuer != "", authInfo.Scope != ""
	if fillFromResourceMetadata(authInfo, metadata) {
		changed = true
	}
	if discovered {
		if trimmed := strings.TrimSuffix(authInfo.Issuer, "/"); trimmed != authInfo.Issuer {
			authInfo.Issuer = trimmed
			changed = true
		}
	}
	if !hadIssuer && authInfo.Issuer != "" {
		logging.Info("AuthTools", "Discovered authorization server for %s: %s", info.Name, authInfo.Issuer)
	}
	if !hadScope && authInfo.Scope != "" {
		logging.Info("AuthTools", "Discovered required scope for %s: %s", info.Name, authInfo.Scope)
	}
	if changed {
		info.SetAuthInfo(authInfo)
	}
	if !discovered {
		return authInfo, nil
	}

	a.discovered.remember(info.Name, info.URL, authInfo)
	if key := api.ServerIssuerKey(info.Name, authInfo.Issuer); key != "" {
		// A tenant server's owner record names every key its sessions use,
		// so a sibling's deletion does not sweep the person's grant away.
		if err := a.recordTenantIssuer(ctx, info, key); err != nil {
			logging.WarnWithAttrs("Aggregator", "Cannot record the discovered issuer with the server's namespace",
				slog.String("server", info.Name),
				slog.String("error", err.Error()))
		}
	}
	if previous != nil && !sameAuthorizationServer(previous, discoveredDescription(info)) {
		a.releaseAuthorizationServerDescription(ctx, info.Name, previous)
	}
	// The server has its issuer now: its description is pinned under it
	// before any flow starts or any grant is looked up there.
	if err := a.pinDiscoveredIssuer(ctx, info); err != nil {
		return nil, err
	}
	return authInfo, nil
}
