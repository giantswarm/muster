package aggregator

import (
	"context"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// definitionPinTimeout bounds one pin from a definition: the client
// credentials Secret read behind it.
const definitionPinTimeout = 10 * time.Second

// definedServers returns the servers the MCPServer definitions describe, in
// whatever state their connection is, shaped as the registry would hold them
// (name, namespace, UID, creation time, URL, auth config, and the issuer
// learned for a server that discovers a subject-scoped one). Nil without a
// service registry.
func (am *AggregatorManager) definedServers() map[string]*ServerInfo {
	if am.serviceRegistry == nil {
		return nil
	}
	defined := make(map[string]*ServerInfo)
	for _, service := range am.serviceRegistry.GetByType(api.TypeMCPServer) {
		data := service.GetServiceData()
		if data == nil {
			continue
		}
		auth, _ := data["auth"].(*api.MCPServerAuth)
		namespace, _ := data["namespace"].(string)
		url, _ := data["url"].(string)
		uid, _ := data[api.ServiceDataUID].(string)
		createdAt, _ := data[api.ServiceDataCreatedAt].(time.Time)
		info := &ServerInfo{
			Name:       service.GetName(),
			Namespace:  namespace,
			UID:        uid,
			CreatedAt:  createdAt,
			URL:        url,
			AuthConfig: auth,
		}
		if am.aggregatorServer != nil {
			am.aggregatorServer.rememberedDefinition(info)
		}
		defined[service.GetName()] = info
	}
	return defined
}

// pinDefinedAuthorizationServers hands every MCPServer definition's
// authorization server to the OAuth handler, whether or not its backend
// answers: a subject-scoped grant is released by FetchSecret and the broker
// as soon as the definition exists, not only once the backend connects or
// answers 401. A definition that changed or went releases the description it
// was pinned with (releaseAuthorizationServerDescription). It also makes every
// replica learn and pin the issuer a server with subject-scoped grants
// discovers (reconcileDiscoveredIssuer).
//
// Each description is pinned once; a pin that fails (the Secret not there
// yet, OAuth not ready) is retried on the next call. Logins re-pin as before,
// which picks up a rotated Secret.
func (am *AggregatorManager) pinDefinedAuthorizationServers(ctx context.Context) {
	if am.aggregatorServer == nil || ctx == nil {
		return
	}
	am.definitionPinsMu.Lock()
	defer am.definitionPinsMu.Unlock()

	// The provider Apps first: an MCPServer that names an App's issuer
	// leaves the App's pin alone.
	am.aggregatorServer.providerApps.pinAll(ctx)

	defined := am.definedServers()
	for name, pinned := range am.definitionPins {
		var current *api.MCPServerAuthAuthorizationServer
		if info, ok := defined[name]; ok {
			current = authorizationServerOf(info.AuthConfig)
		}
		if sameAuthorizationServer(pinned, current) {
			continue
		}
		delete(am.definitionPins, name)
		releaseCtx, cancel := context.WithTimeout(ctx, definitionPinTimeout)
		am.aggregatorServer.releaseAuthorizationServerDescription(releaseCtx, name, pinned)
		cancel()
	}

	for name, info := range defined {
		if info.AuthConfig.DiscoversSubjectScopedIssuer() {
			// Discovered descriptions are pinned in each process that
			// learns them, not tracked here: a replica that never learned
			// the issuer looks it up, one that knows it pins it.
			pinCtx, cancel := context.WithTimeout(ctx, definitionPinTimeout)
			am.aggregatorServer.reconcileDiscoveredIssuer(pinCtx, info)
			cancel()
			continue
		}
		as := authorizationServerOf(info.AuthConfig)
		if as == nil || am.definitionPins[name] != nil {
			continue
		}
		pinCtx, cancel := context.WithTimeout(ctx, definitionPinTimeout)
		err := am.aggregatorServer.pinAuthorizationServer(pinCtx, info)
		cancel()
		if err != nil {
			logPinDeferred(info, err)
			continue
		}
		pinned := *as
		am.definitionPins[name] = &pinned
	}
}
