package testing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// brokerTrustedServerRefs returns the set of mock OAuth server names referenced by
// a muster_broker block (as trusted issuers, workload group grants, or actor
// delegation grants). Those servers must sign their tokens and serve TLS so
// muster's broker can validate them via JWKS.
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
	for _, g := range b.WorkloadGroupGrants {
		if g.OAuthServerRef != "" {
			refs[g.OAuthServerRef] = true
		}
	}
	for _, d := range b.ActorDelegationPolicy {
		if d.ActorOAuthServerRef != "" && d.ActorOAuthServerRef != "*" {
			refs[d.ActorOAuthServerRef] = true
		}
		if d.SubjectOAuthServerRef != "" && d.SubjectOAuthServerRef != "*" {
			refs[d.SubjectOAuthServerRef] = true
		}
	}
	return refs
}

// generateBrokerSigningKey writes an EC P-256 private key (EC PRIVATE KEY PEM) to
// the muster config directory and returns its path. muster's loadSigningKey reads
// this to sign local-mint JWTs (ES256).
func generateBrokerSigningKey(musterConfigPath string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generating broker signing key: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("marshaling broker signing key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	path := filepath.Join(musterConfigPath, "broker-signing-key.pem")
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		return "", fmt.Errorf("writing broker signing key: %w", err)
	}
	return path, nil
}

// applyBrokerConfig populates the muster oauth.server config map with JWT mode,
// trusted issuers and the local-mint token-exchange broker, resolving mock-server
// references to live issuer/JWKS URLs. It is a no-op when no muster_broker is set.
func (m *musterInstanceManager) applyBrokerConfig(
	serverConfig map[string]interface{},
	config *MusterPreConfiguration,
	port int,
	instanceID, musterConfigPath string,
	logger TestLogger,
) error {
	if config == nil || config.MusterBroker == nil {
		return nil
	}
	broker := config.MusterBroker
	baseURL := fmt.Sprintf("http://localhost:%d", port)

	keyPath, err := generateBrokerSigningKey(musterConfigPath)
	if err != nil {
		return err
	}
	serverConfig["enableJWTMode"] = true
	serverConfig["jwtSigningKeyFile"] = keyPath
	serverConfig["resourceIdentifier"] = baseURL

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

	tokenExchangeBroker, err := m.buildTokenExchangeBrokerConfig(broker, instanceID)
	if err != nil {
		return err
	}
	serverConfig["tokenExchangeBroker"] = tokenExchangeBroker

	if m.debug {
		logger.Debug("🔐 Configured muster token-exchange broker (local-mint): %d trusted issuers, %d targets\n",
			len(broker.TrustedIssuers), len(broker.Targets))
	}
	return nil
}

// buildTokenExchangeBrokerConfig assembles the tokenExchangeBroker config map from
// the scenario broker spec, resolving issuer references for grants.
func (m *musterInstanceManager) buildTokenExchangeBrokerConfig(
	broker *MusterBrokerConfig,
	instanceID string,
) (map[string]interface{}, error) {
	targets := make(map[string]interface{}, len(broker.Targets))
	for audience, targetType := range broker.Targets {
		if targetType == "" {
			targetType = "local-mint"
		}
		targets[audience] = map[string]interface{}{"type": targetType}
	}

	result := map[string]interface{}{
		"targets":        targets,
		"delegateToSelf": broker.DelegateToSelf,
	}

	if len(broker.WorkloadAudiences) > 0 {
		result["workloadAudiences"] = broker.WorkloadAudiences
	}

	if len(broker.WorkloadGroupGrants) > 0 {
		grants := make([]map[string]interface{}, 0, len(broker.WorkloadGroupGrants))
		for _, g := range broker.WorkloadGroupGrants {
			issuerURL := m.resolveIssuerURL(instanceID, g.OAuthServerRef)
			if issuerURL == "" {
				return nil, fmt.Errorf("muster_broker workload_group_grant references unknown OAuth server %q", g.OAuthServerRef)
			}
			grant := map[string]interface{}{
				"issuer":    issuerURL,
				"subject":   g.Subject,
				"audiences": g.Audiences,
			}
			if g.GrantedSubject != "" || len(g.GrantedGroups) > 0 {
				granted := map[string]interface{}{}
				if g.GrantedSubject != "" {
					granted["subject"] = g.GrantedSubject
				}
				if len(g.GrantedGroups) > 0 {
					granted["groups"] = g.GrantedGroups
				}
				grant["granted"] = granted
			}
			grants = append(grants, grant)
		}
		result["workloadGroupGrants"] = grants
	}

	if len(broker.ActorDelegationPolicy) > 0 {
		policy := make([]map[string]interface{}, 0, len(broker.ActorDelegationPolicy))
		for _, d := range broker.ActorDelegationPolicy {
			policy = append(policy, map[string]interface{}{
				"actorIssuer":    m.resolveIssuerRefOrWildcard(instanceID, d.ActorOAuthServerRef),
				"actorSubject":   d.ActorSubject,
				"subjectIssuer":  m.resolveIssuerRefOrWildcard(instanceID, d.SubjectOAuthServerRef),
				"subjectSubject": d.SubjectSubject,
			})
		}
		result["actorDelegationPolicy"] = policy
	}

	return result, nil
}

// resolveIssuerURL returns the live issuer URL of a started mock OAuth server.
func (m *musterInstanceManager) resolveIssuerURL(instanceID, ref string) string {
	srv := m.GetMockOAuthServer(instanceID, ref)
	if srv == nil {
		return ""
	}
	return srv.GetIssuerURL()
}

// resolveIssuerRefOrWildcard resolves a server ref to its issuer URL, passing "*"
// (any issuer) and empty through unchanged.
func (m *musterInstanceManager) resolveIssuerRefOrWildcard(instanceID, ref string) string {
	if ref == "" || ref == "*" {
		return "*"
	}
	return m.resolveIssuerURL(instanceID, ref)
}
