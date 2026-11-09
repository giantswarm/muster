package testing

import (
	"crypto/rand"
	"fmt"
)

// testResourceBearer authenticates the harness without creating provider grants.
func (m *musterInstanceManager) testResourceBearer(instanceID, serverName string, port int) (string, error) {
	issuer := m.GetMockOAuthServer(instanceID, serverName)
	if issuer == nil {
		return "", fmt.Errorf("mock issuer %q is unavailable", serverName)
	}
	return issuer.MintSignedJWT(map[string]any{
		"sub": "test-user-123", "aud": fmt.Sprintf("http://localhost:%d", port),
		"client_id": "muster-test-harness", "jti": rand.Text(),
		"email": "test@example.com", "email_verified": true,
		"scope": "openid profile email",
	}, "at+jwt")
}
