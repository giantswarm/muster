package testing

import (
	"fmt"
	"os"
	"path/filepath"
)

// brokerTrustedServerRefs returns the set of mock OAuth server names referenced
// by a muster_broker block as trusted issuers. Those servers must sign their
// tokens and serve TLS so muster's exchange can validate them via JWKS.
func brokerTrustedServerRefs(config *MusterPreConfiguration) map[string]bool {
	refs := make(map[string]bool)
	if config == nil || config.MusterBroker == nil {
		return refs
	}
	b := config.MusterBroker
	for _, ti := range b.TrustedIssuers {
		if ti.OAuthServerRef != "" {
			refs[ti.OAuthServerRef] = true
		}
	}
	return refs
}

// applyBrokerConfig populates the muster oauth.server config map with the
// trusted issuers whose tokens muster accepts as bearers on /mcp, resolving
// mock-server references to live issuer/JWKS URLs. muster never signs tokens
// (no JWT mode); dex stand-ins are the only issuers. It is a no-op when no
// muster_broker is set.
func (m *musterInstanceManager) applyBrokerConfig(
	serverConfig map[string]interface{},
	config *MusterPreConfiguration,
	port int,
	instanceID string,
	musterConfigPath string,
	logger TestLogger,
) error {
	if config == nil || config.MusterBroker == nil {
		return nil
	}
	broker := config.MusterBroker
	serverConfig["resourceIdentifier"] = fmt.Sprintf("http://localhost:%d", port)

	// Trusted issuers: resolve each referenced mock server to its issuer + JWKS URL.
	trustedIssuers := make([]map[string]interface{}, 0, len(broker.TrustedIssuers))
	for _, ti := range broker.TrustedIssuers {
		issuerURL := m.resolveIssuerURL(instanceID, ti.OAuthServerRef)
		if issuerURL == "" {
			return fmt.Errorf("muster_broker trusted issuer references unknown OAuth server %q", ti.OAuthServerRef)
		}
		entry := map[string]interface{}{
			"issuer":             issuerURL,
			"jwksUrl":            issuerURL + "/jwks",
			"allowPrivateIPJWKS": true,
		}
		if len(ti.AllowedAudiences) > 0 {
			entry["allowedAudiences"] = ti.AllowedAudiences
		}
		if len(ti.AllowedClaims) > 0 {
			entry["allowedClaims"] = ti.AllowedClaims
		}
		if ti.SubjectClaim != "" {
			entry["subjectClaim"] = ti.SubjectClaim
		}
		if ti.AcceptedTypHeaders != nil {
			entry["acceptedTypHeaders"] = ti.AcceptedTypHeaders
		}
		trustedIssuers = append(trustedIssuers, entry)
	}
	serverConfig["trustedIssuers"] = trustedIssuers

	// Brokered exchange: confidential clients and the audiences they may
	// request, plus grant targets that release a person's grant from a mock
	// authorization server.
	if len(broker.Clients) > 0 || len(broker.GrantTargets) > 0 {
		brokerCfg := map[string]interface{}{}
		if len(broker.Clients) > 0 {
			clientAudiences := map[string]interface{}{}
			brokerClients := map[string]interface{}{}
			for _, c := range broker.Clients {
				if c.ClientID == "" || c.ClientSecret == "" {
					return fmt.Errorf("muster_broker.clients entries need client_id and client_secret")
				}
				secretFile := filepath.Join(musterConfigPath, "broker-client-"+sanitizeFileName(c.ClientID)+".secret")
				if err := os.WriteFile(secretFile, []byte(c.ClientSecret), 0o600); err != nil {
					return fmt.Errorf("write broker client secret for %q: %w", c.ClientID, err)
				}
				clientAudiences[c.ClientID] = c.Audiences
				brokerClients[c.ClientID] = map[string]interface{}{"clientSecretFile": secretFile}
			}
			brokerCfg["clientAudiences"] = clientAudiences
			brokerCfg["brokerClients"] = brokerClients
		}
		if len(broker.GrantTargets) > 0 {
			targets := map[string]interface{}{}
			for audience, gt := range broker.GrantTargets {
				issuerURL := m.resolveIssuerURL(instanceID, gt.OAuthServerRef)
				if issuerURL == "" {
					return fmt.Errorf("muster_broker grant target %q references unknown OAuth server %q", audience, gt.OAuthServerRef)
				}
				targets[audience] = map[string]interface{}{"grantIssuer": issuerURL}
			}
			brokerCfg["targets"] = targets
		}
		serverConfig["tokenExchangeBroker"] = brokerCfg
	}

	if m.debug {
		logger.Debug("🔐 Configured muster trusted issuers: %d, broker clients: %d, grant targets: %d\n",
			len(broker.TrustedIssuers), len(broker.Clients), len(broker.GrantTargets))
	}
	return nil
}

// resolveIssuerURL returns the live issuer URL of a started mock OAuth server.
func (m *musterInstanceManager) resolveIssuerURL(instanceID, ref string) string {
	srv := m.GetMockOAuthServer(instanceID, ref)
	if srv == nil {
		return ""
	}
	return srv.GetIssuerURL()
}
