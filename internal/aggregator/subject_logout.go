package aggregator

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/giantswarm/muster/pkg/logging"
)

// sameIssuer compares two issuer URLs the way the OAuth client files pins:
// a trailing slash does not make a different authorization server.
func sameIssuer(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}

// serversOfIssuer returns the names of the registered servers whose OAuth
// issuer is issuer and whose connections are made from the person's grant --
// SSO servers (token forwarding, token exchange) are connected from muster's
// own session token and never hold that grant, so they are left out.
func (a *AggregatorServer) serversOfIssuer(issuer string) []string {
	var names []string
	for name, info := range a.registry.GetAllServers() {
		// The pin counts too: after a restart a sibling's entry has no
		// AuthInfo until someone logs in, but its pooled clients and auth
		// marks from before the restart are as live as any.
		known := knownServerIssuer(info)
		if known == "" || !sameIssuer(known, issuer) {
			continue
		}
		if ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// disconnectSessionServer drops what lets one session call tools on one
// server: the auth mark, the cached capabilities and the pooled client. The
// token store is not touched -- callers decide the scope of that separately.
func (a *AggregatorServer) disconnectSessionServer(ctx context.Context, sessionID, serverName string) {
	if a.authStore != nil {
		if err := a.authStore.Revoke(ctx, sessionID, serverName); err != nil {
			logging.Warn("AuthTools", "Failed to revoke auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}
	if a.capabilityStore != nil {
		if err := a.capabilityStore.DeleteEntry(ctx, sessionID, serverName); err != nil {
			logging.Warn("AuthTools", "Failed to delete entry %s/%s from capability store: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}
	if a.connPool != nil {
		a.connPool.Evict(sessionID, serverName)
	}
}

// disconnectIssuerForSubject finishes a sign-out from a subject-scoped issuer
// after the person's grant was deleted from the token store: every server of
// the issuer is disconnected for the calling session and for every other live
// session of the same person this replica knows, so no pooled client keeps
// using the token the person just revoked and no session re-adopts the grant.
// Sessions on other replicas, or ones this replica has not seen a request
// from, are not reached here; their pooled clients find the token gone on
// their next request (the token store is consulted before every call) and
// disconnect themselves through the auth-loss handler, and a fresh
// connection attempt finds no grant to adopt.
//
// The person's live transport sessions get list_changed notifications so
// clients that cache the tool list drop the hidden tools.
//
// Returns the servers other than serverName that were disconnected for the
// calling session, sorted, for the tool result.
func (a *AggregatorServer) disconnectIssuerForSubject(ctx context.Context, sessionID, sub, serverName, issuer string) []string {
	servers := a.serversOfIssuer(issuer)

	sessions := []string{sessionID}
	if a.subjectSessions != nil {
		for _, other := range a.subjectSessions.OAuthSessionIDsForSubject(sub) {
			if other != sessionID {
				sessions = append(sessions, other)
			}
		}
	}

	for _, sid := range sessions {
		for _, name := range servers {
			a.disconnectSessionServer(ctx, sid, name)
		}
		if a.ssoTracker != nil {
			for _, name := range servers {
				a.ssoTracker.ClearSSOFailed(sub, name)
			}
		}
	}

	logging.InfoWithAttrs("AuthTools", "subject_grant_revoked",
		slog.String("issuer", issuer),
		slog.String("server", serverName),
		slog.String("subject", logging.TruncateIdentifier(sub)),
		slog.Int("servers", len(servers)),
		slog.Int("sessions", len(sessions)))

	a.notifySubjectListsChanged(sub)

	var siblings []string
	for _, name := range servers {
		if name != serverName {
			siblings = append(siblings, name)
		}
	}
	return siblings
}

// notifySubjectListsChanged tells every live transport session of sub that
// its tools, resources and prompts changed. Used after a sign-out hid a
// server's capabilities; the connect path's notifySubjectCapabilitiesChanged
// is the counterpart, sized by what appeared. Nothing here knows what
// disappeared, so all three lists are announced; a client re-listing an
// unchanged list gets the same list back.
func (a *AggregatorServer) notifySubjectListsChanged(sub string) {
	if a.mcpServer == nil || a.subjectSessions == nil || sub == "" {
		return
	}
	for _, sessionID := range a.subjectSessions.GetSessionIDs(sub) {
		for _, category := range capabilityCategories {
			if err := a.mcpServer.SendNotificationToSpecificClient(sessionID, category.method, nil); err != nil {
				logging.Debug("Aggregator", "%s to session %s failed: %v",
					category.method, logging.TruncateIdentifier(sessionID), err)
			}
		}
	}
}
