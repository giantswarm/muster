package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestApprovalTrustedClients_KeepsTheHandoffClientsAlias(t *testing.T) {
	var cfg ApprovalConfig
	assert.NoError(t, yaml.Unmarshal([]byte("trustedClients: [portal]\nhandoffClients: [portal, legacy]\n"), &cfg))
	assert.Equal(t, []string{"portal", "legacy"}, cfg.TrustedClientIDs())

	cfg = ApprovalConfig{HandoffClients: []string{"portal"}}
	assert.Equal(t, []string{"portal"}, cfg.TrustedClientIDs(), "the earlier name alone still trusts")
	assert.Empty(t, ApprovalConfig{}.TrustedClientIDs())
}
