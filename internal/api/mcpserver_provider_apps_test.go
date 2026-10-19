package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateProviderApps(t *testing.T) {
	assert.NoError(t, ValidateProviderApps(nil))
	assert.NoError(t, ValidateProviderApps(&MCPServerAuth{}))
	assert.NoError(t, ValidateProviderApps(&MCPServerAuth{ForwardToken: true, ProviderApps: []string{"github"}}))
	assert.NoError(t, ValidateProviderApps(&MCPServerAuth{ForwardIdentity: true, ProviderApps: []string{"github"}}))
	assert.Error(t, ValidateProviderApps(&MCPServerAuth{Type: MCPServerAuthTypeOAuth, ProviderApps: []string{"github"}}))

	assert.True(t, (&MCPServerAuth{ForwardToken: true, ProviderApps: []string{"github"}}).ReceivesProviderApps())
	assert.False(t, (&MCPServerAuth{ProviderApps: []string{"github"}}).ReceivesProviderApps())
	assert.False(t, (&MCPServerAuth{ForwardToken: true}).ReceivesProviderApps())
	assert.False(t, (*MCPServerAuth)(nil).ReceivesProviderApps())
}
