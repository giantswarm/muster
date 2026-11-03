package aggregator

import (
	"context"
	"log/slog"
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// A server that discovers its authorization server through RFC 9728 and files
// its grants under the person (spec.auth.grantScope: subject, no
// spec.auth.authorizationServer.issuer) is subject-scoped for the issuer
// muster discovers for it. The decision lives in the MCPServer spec, which
// muster always has; what it applies to -- the issuer -- is learned from the
// server's resource metadata, and is not kept anywhere but on the registry
// entry. So muster learns it as soon as the server registers (here, when the
// 401 did not name it), and again wherever it is needed and still unknown
// (resolveServerAuthInfo), and pins the description of that issuer
// (pinDiscoveredIssuer): after a restart the person's persisted grant is
// reached the same way as before it.

// discoveredIssuerTimeout bounds the resource-metadata lookup a registration
// makes for the issuer of such a server.
const discoveredIssuerTimeout = 10 * time.Second

// withDiscoveredIssuer returns the registration of a server that discovers a
// subject-scoped issuer (api.MCPServerAuth.DiscoversSubjectScopedIssuer) with
// the issuer, scope and resource of its RFC 9728 metadata filled in where the
// 401 left them out: the 401 the backend answered muster's probe with names
// the metadata at best, not the issuer. The issuer is then known, and pinned,
// before the first session of anyone looks for the person's grant. A failed
// lookup is logged and leaves the registration as it was; the issuer is
// resolved again on first use. Any other registration is returned unchanged.
func withDiscoveredIssuer(ctx context.Context, registration PendingAuthRegistration) PendingAuthRegistration {
	if !registration.AuthConfig.DiscoversSubjectScopedIssuer() || registration.URL == "" {
		return registration
	}
	if registration.AuthInfo != nil && registration.AuthInfo.Issuer != "" {
		return registration
	}
	ctx, cancel := context.WithTimeout(ctx, discoveredIssuerTimeout)
	defer cancel()
	metadata, err := discoverProtectedResourceMetadata(ctx, registration.URL, nil)
	if err != nil || metadata.Issuer == "" {
		reason := "the metadata names no authorization server"
		if err != nil {
			reason = err.Error()
		}
		logging.WarnWithAttrs("Aggregator", "Cannot discover the authorization server of a subject-scoped server at registration; it is resolved again on first use",
			slog.String("server", registration.Name),
			slog.String("error", reason))
		return registration
	}

	authInfo := &AuthInfo{}
	if registration.AuthInfo != nil {
		copied := *registration.AuthInfo
		authInfo = &copied
	}
	authInfo.Issuer = metadata.Issuer
	if authInfo.Scope == "" {
		authInfo.Scope = metadata.Scope
	}
	if authInfo.Resource == "" {
		authInfo.Resource = metadata.Resource
	}
	registration.AuthInfo = authInfo
	logging.InfoWithAttrs("Aggregator", "Discovered the authorization server of a subject-scoped server",
		slog.String("server", registration.Name),
		slog.String("issuer", metadata.Issuer))
	return registration
}

// subjectGrantIssuer returns the issuer key and scope a session finds the
// person's grant for the server under (sessionOAuthIssuer), resolving the
// server's auth info when the registry entry knows no issuer yet -- a server
// that discovers its subject-scoped issuer and whose metadata could not be
// read when it registered -- and making sure the description of a discovered
// issuer is pinned (pinDiscoveredIssuer). An empty issuer means the grant
// cannot be looked up; an error, that the server conflicts with the
// description pinned under its issuer key.
func (a *AggregatorServer) subjectGrantIssuer(ctx context.Context, info *ServerInfo) (issuer, scope string, err error) {
	issuer, scope = sessionOAuthIssuer(info)
	if issuer == "" && info.AuthConfig.DiscoversSubjectScopedIssuer() {
		// The resolution records what it learns on the entry.
		if _, err := a.resolveServerAuthInfo(ctx, info); err != nil {
			return "", "", err
		}
		issuer, scope = sessionOAuthIssuer(info)
	}
	if issuer == "" {
		return "", "", nil
	}
	if err := a.pinDiscoveredIssuer(ctx, info); err != nil {
		return "", "", err
	}
	return issuer, scope, nil
}
