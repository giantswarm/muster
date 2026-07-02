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

// generateBrokerSigningKey writes an EC P-256 private key (EC PRIVATE KEY PEM) to
// the muster config directory and returns its path. muster's loadSigningKey reads
// this to sign issued JWTs (ES256).
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

// applyBrokerConfig populates the muster oauth.server config map with JWT mode
// and trusted issuers for the self-issued token exchange, resolving mock-server
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

	if m.debug {
		logger.Debug("🔐 Configured muster token exchange: %d trusted issuers\n",
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
