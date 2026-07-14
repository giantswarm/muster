package testing

import (
	"fmt"
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

	if m.debug {
		logger.Debug("🔐 Configured muster trusted issuers: %d\n",
			len(broker.TrustedIssuers))
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
