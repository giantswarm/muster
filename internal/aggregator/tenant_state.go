package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The stored sign-ins of an organization namespace belong to the MCPServer
// objects that made them, not to the namespace's name. A namespace deleted
// and created again with the same name, or a server of it deleted, must
// never hand the people's tokens and grants, the client registrations or
// the flows of the old servers to a new one (D9).
//
// muster keys that state by (namespace, issuer) (api.IssuerKey), so it
// records which objects own a namespace's state: their Kubernetes UIDs
// (oauthstore.TenantOwnerStore). Two moments forget it:
//
//   - A server's definition goes (forgetTenantServer, on deregistration):
//     when it was the namespace's last, everything of the namespace goes;
//     otherwise its issuer keys go unless a server that is left names them.
//   - A server registers (adoptTenantServer) whose UID is not recorded,
//     in a namespace none of whose recorded servers still exists: it is a
//     server of a new namespace of the same name -- the old one went while
//     muster was down, or before this record existed -- and everything of
//     the namespace goes before the server can be signed in to.
//
// The second is what makes it safe: it runs before every first
// registration of a tenant server, so no new server can reach the old
// state even when muster never saw the deletion. Servers of muster's
// namespace have no namespace in their key and are never touched.

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
// of its namespace's stored state, forgetting that state first when it
// belonged to servers that no longer exist. A server that takes the key of a
// gone one in a namespace that kept other servers (deleted and created again
// while muster was down) forgets the state of that key: its sign-in marks
// and the issuer keys it names (issuerKeys). An error leaves the server
// unregistered (the caller's registration fails and is retried), so a
// server never serves on state that might be another namespace's.
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
	if _, ok := owners[reg.UID]; ok {
		return nil
	}
	live, err := tenantDefinitionUIDs(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list the MCPServers of namespace %s: %w", namespace, err)
	}

	var gone []string
	liveOwner := false
	for uid := range owners {
		if live[uid] {
			liveOwner = true
		} else {
			gone = append(gone, uid)
		}
	}
	if !liveOwner {
		servers := append(ownerServers(owners), reg.Name)
		if err := a.forgetTenantNamespace(ctx, namespace, servers, "new namespace of the same name"); err != nil {
			return err
		}
	} else if len(gone) > 0 {
		for _, uid := range gone {
			if owners[uid] != reg.Name {
				continue
			}
			if len(issuerKeys) > 0 {
				if err := forgetTenantIssuers(ctx, namespace, issuerKeys); err != nil {
					return err
				}
			}
			if err := a.forgetServerSessions(ctx, reg.Name); err != nil {
				return err
			}
			break
		}
		if err := a.tenantOwners.RemoveOwners(ctx, namespace, gone...); err != nil {
			return fmt.Errorf("forget the gone owners of namespace %s: %w", namespace, err)
		}
	}
	return a.tenantOwners.AddOwner(ctx, namespace, reg.UID, reg.Name)
}

// forgetTenantServer runs when the definition of a server outside muster's
// namespace went (its service was removed): info is its last registry
// entry, or nil. When no server of the namespace is left, everything of the
// namespace goes; otherwise the server's issuer keys go unless a server
// that is left names them. A failure is logged only: the next
// adoptTenantServer in the namespace forgets what this one could not.
func (a *AggregatorServer) forgetTenantServer(ctx context.Context, name string, info *ServerInfo) {
	namespace, _ := api.SplitMCPServerKey(name)
	if namespace == "" || a.tenantOwners == nil {
		return
	}
	definitions, err := tenantDefinitions(ctx, namespace)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Cannot tell which servers of a namespace are left; its sign-ins are forgotten at the next registration there",
			slog.String("server", name), slog.String("error", err.Error()))
		return
	}
	owners, err := a.tenantOwners.Owners(ctx, namespace)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Cannot read the owners of a namespace",
			slog.String("server", name), slog.String("error", err.Error()))
		return
	}

	if len(definitions) == 0 {
		servers := append(ownerServers(owners), name)
		if err := a.forgetTenantNamespace(ctx, namespace, servers, "last server deleted"); err != nil {
			logging.WarnWithAttrs("Aggregator", "Cannot forget the sign-ins of a namespace",
				slog.String("namespace", namespace), slog.String("error", err.Error()))
		}
		return
	}

	var gone []string
	for uid := range owners {
		if _, ok := definitions[uid]; !ok {
			gone = append(gone, uid)
		}
	}
	if err := a.tenantOwners.RemoveOwners(ctx, namespace, gone...); err != nil {
		logging.WarnWithAttrs("Aggregator", "Cannot forget the gone owners of a namespace",
			slog.String("namespace", namespace), slog.String("error", err.Error()))
	}

	inUse := a.tenantIssuerKeysInUse(definitions)
	var forget []string
	for _, key := range serverIssuerKeys(name, info) {
		if !inUse[key] {
			forget = append(forget, key)
		}
	}
	if len(forget) == 0 {
		return
	}
	if err := forgetTenantIssuers(ctx, namespace, forget); err != nil {
		logging.WarnWithAttrs("Aggregator", "Cannot forget the sign-ins of a deleted server",
			slog.String("server", name), slog.String("error", err.Error()))
		return
	}
	logging.InfoWithAttrs("Aggregator", "tenant_signins_forgotten",
		slog.String("server", name),
		slog.String("issuerKeys", strings.Join(forget, ",")),
		slog.String("reason", "server deleted"))
}

// forgetTenantNamespace forgets everything muster stores for a namespace:
// the OAuth state under every issuer key of it, and the sessions' sign-in
// marks, capabilities and pooled clients of the given servers. Then the
// namespace has no owners.
func (a *AggregatorServer) forgetTenantNamespace(ctx context.Context, namespace string, servers []string, reason string) error {
	if err := forgetTenantIssuers(ctx, namespace, nil); err != nil {
		return err
	}
	for _, server := range servers {
		if err := a.forgetServerSessions(ctx, server); err != nil {
			return err
		}
	}
	if err := a.tenantOwners.DeleteNamespace(ctx, namespace); err != nil {
		return fmt.Errorf("forget the owners of namespace %s: %w", namespace, err)
	}
	logging.InfoWithAttrs("Aggregator", "tenant_signins_forgotten",
		slog.String("namespace", namespace),
		slog.String("reason", reason))
	return nil
}

// forgetServerSessions forgets what the sessions hold for a server key: the
// sign-in marks, the capabilities and the pooled clients.
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

// tenantIssuerKeysInUse returns the issuer keys that the servers left in a
// namespace name: the authorization server their definitions pin, and the
// one their registry entries discovered.
func (a *AggregatorServer) tenantIssuerKeysInUse(definitions map[string]api.MCPServerInfo) map[string]bool {
	inUse := make(map[string]bool)
	for _, definition := range definitions {
		key := definition.ServerKey()
		for _, issuerKey := range serverIssuerKeys(key, &ServerInfo{Name: key, AuthConfig: definition.Auth}) {
			inUse[issuerKey] = true
		}
		if info, ok := a.registry.GetServerInfo(key); ok {
			for _, issuerKey := range serverIssuerKeys(key, info) {
				inUse[issuerKey] = true
			}
		}
	}
	return inUse
}

// serverIssuerKeys returns the issuer keys a server's state is filed under:
// that of its pinned authorization server and that of the one its endpoint
// named.
func serverIssuerKeys(name string, info *ServerInfo) []string {
	if info == nil {
		return nil
	}
	var keys []string
	add := func(issuer string) {
		if issuer == "" {
			return
		}
		if key := api.ServerIssuerKey(name, strings.TrimSuffix(issuer, "/")); key != "" {
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

// ownerServers returns the server keys of a namespace's recorded owners.
func ownerServers(owners map[string]string) []string {
	servers := make([]string, 0, len(owners))
	for _, server := range owners {
		servers = append(servers, server)
	}
	return servers
}

// tenantDefinitions returns the MCPServer definitions of a namespace by UID.
func tenantDefinitions(ctx context.Context, namespace string) (map[string]api.MCPServerInfo, error) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, fmt.Errorf("no MCPServer manager")
	}
	servers, err := manager.ListMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	definitions := make(map[string]api.MCPServerInfo)
	for _, server := range servers {
		if server.Namespace == namespace && server.UID != "" {
			definitions[server.UID] = server
		}
	}
	return definitions, nil
}

// tenantDefinitionUIDs is tenantDefinitions reduced to the UIDs.
func tenantDefinitionUIDs(ctx context.Context, namespace string) (map[string]bool, error) {
	definitions, err := tenantDefinitions(ctx, namespace)
	if err != nil {
		return nil, err
	}
	uids := make(map[string]bool, len(definitions))
	for uid := range definitions {
		uids[uid] = true
	}
	return uids, nil
}
