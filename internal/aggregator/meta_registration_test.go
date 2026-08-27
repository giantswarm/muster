package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRegistrationMeta = map[string]string{"AWS_REGION": "eu-central-1"}

// TestPendingAuthRegistrationCarriesMeta covers the gap that made spec.meta a
// silent no-op for a server that needs a login: its tool calls run on a
// session-scoped client the aggregator builds, not on the one the client
// factory built, so the entries have to travel with the registration.
func TestPendingAuthRegistrationCarriesMeta(t *testing.T) {
	reg := NewServerRegistry("x")
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "needs-login"},
		URL:                "https://mcp.example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		Meta:               testRegistrationMeta,
	}))

	serverInfo, ok := reg.GetServerInfo("needs-login")
	require.True(t, ok)
	assert.Equal(t, testRegistrationMeta, serverInfo.Meta)
}

func TestRegisteredMeta(t *testing.T) {
	reg := NewServerRegistry("x")
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "needs-login"},
		URL:                "https://mcp.example.com/mcp",
		Meta:               testRegistrationMeta,
	}))
	agg := &AggregatorServer{registry: reg}

	assert.Equal(t, testRegistrationMeta, registeredMeta(agg, "needs-login"))

	// establishConnection runs before the registry is guaranteed to hold the
	// server, so an unknown name means no entries rather than a panic.
	assert.Nil(t, registeredMeta(agg, "unknown"))
	assert.Nil(t, registeredMeta(&AggregatorServer{}, "needs-login"))
	assert.Nil(t, registeredMeta(nil, "needs-login"))
}
