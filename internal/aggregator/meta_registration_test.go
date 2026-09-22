package aggregator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRegistrationMeta = map[string]string{"AWS_REGION": "eu-central-1"}

// The hosted GitHub MCP server selects its toolsets per session from this
// header (#1304).
var testRegistrationHeaders = map[string]string{"X-MCP-Toolsets": "default,git"}

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
		Headers:            testRegistrationHeaders,
	}))

	serverInfo, ok := reg.GetServerInfo("needs-login")
	require.True(t, ok)
	assert.Equal(t, testRegistrationMeta, serverInfo.Meta)
	assert.Equal(t, testRegistrationHeaders, serverInfo.Headers, "spec.headers travel with the registration the same way (#1304)")
}

// TestRegisteredServerInfo covers what establishConnection reads from the
// registry for the client it builds with a person's grant: spec.meta and
// spec.timeout. The timeout had the same gap meta once had -- the
// session-scoped client of an OAuth server ran under the default budget
// whatever the server declared.
func TestRegisteredServerInfo(t *testing.T) {
	reg := NewServerRegistry("x")
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "needs-login", Timeout: 90 * time.Second},
		URL:                "https://mcp.example.com/mcp",
		Meta:               testRegistrationMeta,
		Headers:            testRegistrationHeaders,
	}))
	agg := &AggregatorServer{registry: reg}

	info := registeredServerInfo(agg, "needs-login")
	require.NotNil(t, info)
	assert.Equal(t, testRegistrationMeta, info.Meta)
	assert.Equal(t, testRegistrationHeaders, info.Headers)
	assert.Equal(t, 90*time.Second, info.Timeout)

	// establishConnection runs before the registry is guaranteed to hold the
	// server, so an unknown name means no entry rather than a panic.
	assert.Nil(t, registeredServerInfo(agg, "unknown"))
	assert.Nil(t, registeredServerInfo(&AggregatorServer{}, "needs-login"))
	assert.Nil(t, registeredServerInfo(nil, "needs-login"))
}
