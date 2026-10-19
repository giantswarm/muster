package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The stored sign-ins of an organization namespace belong to the MCPServer
// objects that made them, not to the namespace's name. A namespace deleted
// and created again with the same name, or a server of it deleted, must
// never hand the people's tokens and grants, the client registrations or
// the flows of the old servers to a new one (D9).
//
// muster keys that state by (namespace, issuer) (api.IssuerKey), so it
// records which objects own a namespace's state: their Kubernetes UIDs and
// the issuer keys each one's sign-ins are filed under
// (oauthstore.TenantOwnerStore). sweepTenantNamespace compares the record
// with the MCPServer objects that exist:
//
//   - no recorded server exists any more (or none is recorded): everything
//     of the namespace goes;
//   - some are gone: the issuer keys only gone servers used go.
//
// It runs when a tenant server's service is removed (forgetTenantServer)
// and before a server with an unrecorded UID registers (adoptTenantServer).
// The second makes it safe: no new server can reach the old state, even
// when muster never saw the deletion (it was down, or the delete and the
// create reached it as one update). A restart registers recorded UIDs and
// forgets nothing. Servers of muster's namespace have no namespace in their
// key and are never touched.
//
// Replicas share the record. Each one removes only the UIDs it read, so a
// server another replica recorded meanwhile stays recorded; a replica that
// lists the objects late can still forget a namespace another one just
// adopted a server in, which signs people out, never in.

// tenantForgetTimeout bounds one adoption or forget: a listing of the
// MCPServers and the store writes behind it.
const tenantForgetTimeout = 30 * time.Second

// pendingIssuerKeys returns the issuer keys a pending-auth registration
// names: its pinned authorization server and the one its endpoint's 401
// named.
func pendingIssuerKeys(reg PendingAuthRegistration) []string {
	info := &ServerInfo{Name: reg.Name, AuthConfig: reg.AuthConfig, AuthInfo: reg.AuthInfo}
	return serverIssuerKeys(reg.Name, info)
}

// adoptTenantServer records a server outside muster's namespace as an owner
// of its namespace's stored state, with the issuer keys it names, after
// sweeping the namespace when its UID is not recorded yet. An error leaves
// the server unregistered (the caller's registration fails and is retried),
// so a server never serves on state that might be another namespace's.
func (a *AggregatorServer) adoptTenantServer(ctx context.Context, reg ServerRegistration, issuerKeys []string) error {
	namespace, _ := api.SplitMCPServerKey(reg.Name)
	if namespace == "" || a.tenantOwners == nil {
		return nil
	}
	if reg.UID == "" {
		return fmt.Errorf("server %s outside muster's namespace has no UID: its sign-ins cannot be told apart from those of an earlier server of the same name", reg.Name)
	}
	owners, err := a.tenantOwners.Owners(ctx, namespace)
	if err != nil {
		return fmt.Errorf("read the owners of namespace %s: %w", namespace, err)
	}
	if owner, ok := owners[reg.UID]; ok {
		return a.recordIssuerKeys(ctx, namespace, reg.UID, owner, issuerKeys)
	}
	live, err := tenantDefinitionUIDs(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list the MCPServers of namespace %s: %w", namespace, err)
	}
	if err := a.sweepTenantNamespace(ctx, namespace, owners, live, reg.Name); err != nil {
		return err
	}
	return a.recordIssuerKeys(ctx, namespace, reg.UID, oauthstore.TenantOwner{Server: reg.Name}, issuerKeys)
}

// recordTenantIssuer records that a tenant server's sign-ins are filed under
// issuerKey, before a flow or a token can be stored under it: a key learned
// at the login (discovered, not pinned) is forgotten with the server too.
func (a *AggregatorServer) recordTenantIssuer(ctx context.Context, info *ServerInfo, issuerKey string) error {
	namespace, _ := api.SplitMCPServerKey(info.Name)
	if namespace == "" || a.tenantOwners == nil {
		return nil
	}
	owners, err := a.tenantOwners.Owners(ctx, namespace)
	if err != nil {
		return fmt.Errorf("read the owners of namespace %s: %w", namespace, err)
	}
	owner, ok := owners[info.UID]
	if !ok {
		// Another replica's sweep removed the record (it listed the objects
		// before this one existed): adopt the server again.
		return a.adoptTenantServer(ctx, ServerRegistration{Name: info.Name, UID: info.UID}, []string{issuerKey})
	}
	return a.recordIssuerKeys(ctx, namespace, info.UID, owner, []string{issuerKey})
}

// recordIssuerKeys adds keys to a server's record and writes it when it
// changed (or is new).
func (a *AggregatorServer) recordIssuerKeys(ctx context.Context, namespace, uid string, owner oauthstore.TenantOwner, keys []string) error {
	changed := owner.IssuerKeys == nil
	for _, key := range keys {
		if key != "" && !slices.Contains(owner.IssuerKeys, key) {
			owner.IssuerKeys = append(owner.IssuerKeys, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := a.tenantOwners.SetOwner(ctx, namespace, uid, owner); err != nil {
		return fmt.Errorf("record %s as an owner of namespace %s: %w", owner.Server, namespace, err)
	}
	return nil
}

// forgetTenantServer runs when the service of a server outside muster's
// namespace was removed (its definition deleted or refused). A failure is
// logged only: the next adoption in the namespace sweeps it again.
func (a *AggregatorServer) forgetTenantServer(ctx context.Context, name string) {
	namespace, _ := api.SplitMCPServerKey(name)
	if namespace == "" || a.tenantOwners == nil {
		return
	}
	err := func() error {
		live, err := tenantDefinitionUIDs(ctx, namespace)
		if err != nil {
			return fmt.Errorf("list the MCPServers of namespace %s: %w", namespace, err)
		}
		owners, err := a.tenantOwners.Owners(ctx, namespace)
		if err != nil {
			return fmt.Errorf("read the owners of namespace %s: %w", namespace, err)
		}
		return a.sweepTenantNamespace(ctx, namespace, owners, live, name)
	}()
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Cannot forget the sign-ins of a removed server; the next registration in its namespace does",
			slog.String("server", name), slog.String("error", err.Error()))
	}
}

// sweepTenantNamespace forgets the state of the recorded servers that no
// longer exist (live holds the UIDs that do). With none left, everything of
// the namespace goes, also under keys no record names; otherwise the issuer
// keys only gone servers used. The sessions' marks, capabilities and pooled
// clients of the gone servers go too, and of server, the one being
// registered or removed. Then the gone servers are no owners any more.
func (a *AggregatorServer) sweepTenantNamespace(ctx context.Context, namespace string, owners map[string]oauthstore.TenantOwner, live map[string]bool, server string) error {
	var gone []string
	kept := make(map[string]bool)
	liveServers := make(map[string]bool)
	for uid, owner := range owners {
		if live[uid] {
			liveServers[owner.Server] = true
			for _, key := range owner.IssuerKeys {
				kept[key] = true
			}
		} else {
			gone = append(gone, uid)
		}
	}

	var forget []string
	reason := "server deleted"
	if len(liveServers) == 0 {
		forget, reason = nil, "no recorded server of the namespace exists"
	} else {
		if len(gone) == 0 {
			return nil
		}
		for _, uid := range gone {
			for _, key := range owners[uid].IssuerKeys {
				if !kept[key] && !slices.Contains(forget, key) {
					forget = append(forget, key)
				}
			}
		}
	}
	if len(liveServers) == 0 || len(forget) > 0 {
		if err := forgetTenantIssuers(ctx, namespace, forget); err != nil {
			return err
		}
	}

	servers := []string{server}
	for _, uid := range gone {
		if s := owners[uid].Server; s != "" && !liveServers[s] && !slices.Contains(servers, s) {
			servers = append(servers, s)
		}
	}
	for _, s := range servers {
		if err := a.forgetServerSessions(ctx, s); err != nil {
			return err
		}
	}
	if err := a.tenantOwners.RemoveOwners(ctx, namespace, gone...); err != nil {
		return fmt.Errorf("forget the gone owners of namespace %s: %w", namespace, err)
	}
	logging.InfoWithAttrs("Aggregator", "tenant_signins_forgotten",
		slog.String("namespace", namespace),
		slog.String("issuerKeys", strings.Join(forget, ",")),
		slog.Bool("wholeNamespace", len(liveServers) == 0),
		slog.String("reason", reason))
	return nil
}

// forgetServerSessions forgets what the sessions hold for a server key: the
// sign-in marks, the capabilities, the pooled clients and the SSO records.
func (a *AggregatorServer) forgetServerSessions(ctx context.Context, server string) error {
	a.revokeServerSessions(ctx, server)
	if a.capabilityStore != nil {
		if err := a.capabilityStore.DeleteServer(ctx, server); err != nil {
			return fmt.Errorf("forget the capabilities of %s: %w", server, err)
		}
	}
	if a.ssoTracker != nil {
		a.ssoTracker.ClearServer(server)
	}
	return nil
}

// forgetTenantIssuers asks the OAuth handler to forget the state filed under
// a namespace's issuer keys (all of them when keys is empty). Without OAuth
// nothing is stored.
func forgetTenantIssuers(ctx context.Context, namespace string, keys []string) error {
	handler := api.GetOAuthHandler()
	if handler == nil {
		return nil
	}
	forgetter, ok := handler.(api.TenantIssuerForgetter)
	if !ok {
		return nil
	}
	return forgetter.ForgetTenantIssuers(ctx, namespace, keys)
}

// serverIssuerKeys returns the issuer keys a server's state is filed under:
// that of its pinned authorization server and that of the one its endpoint
// named.
func serverIssuerKeys(name string, info *ServerInfo) []string {
	var keys []string
	add := func(issuer string) {
		if issuer == "" {
			return
		}
		if key := api.ServerIssuerKey(name, strings.TrimSuffix(issuer, "/")); key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if as := authorizationServerOf(info.AuthConfig); as != nil {
		add(as.Issuer)
	}
	if authInfo := info.GetAuthInfo(); authInfo != nil {
		add(authInfo.Issuer)
	}
	return keys
}

// tenantDefinitionUIDs returns the UIDs of the MCPServer objects of a
// namespace.
func tenantDefinitionUIDs(ctx context.Context, namespace string) (map[string]bool, error) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, fmt.Errorf("no MCPServer manager")
	}
	servers, err := manager.ListMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	uids := make(map[string]bool)
	for _, server := range servers {
		if server.Namespace == namespace && server.UID != "" {
			uids[server.UID] = true
		}
	}
	return uids, nil
}
