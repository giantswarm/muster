package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
)

// FamilyOfExposedName answers from the family declarations, not from the
// routing index, so a family tool of auth-protected members is recognised
// before any session listed tools (#1204).
func TestServerRegistry_FamilyOfExposedName(t *testing.T) {
	reg := NewServerRegistry("x")
	registerAuthFamilyMember(t, reg, "mc1-mcp-kubernetes", "kubernetes", "management_cluster")
	registerAuthFamilyMember(t, reg, "mc1-mcp-prometheus", "prometheus", "management_cluster")
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "solo"},
		URL:                "https://solo.example.com",
	}))

	require.False(t, reg.IsFamilyTool("x_kubernetes_list_pods"), "precondition: nothing listed, the index is empty")

	assert.Equal(t, "kubernetes", reg.FamilyOfExposedName("x_kubernetes_list_pods"))
	assert.Equal(t, "prometheus", reg.FamilyOfExposedName("x_prometheus_get_rules"))
	assert.Empty(t, reg.FamilyOfExposedName("x_solo_list"), "a per-server name is not in any family's name space")
	assert.Empty(t, reg.FamilyOfExposedName("x_kubernetes"), "the family prefix alone names no tool")
	assert.Empty(t, reg.FamilyOfExposedName("x_kube_list"), "a prefix that only shares characters with a family does not match")
	assert.Empty(t, reg.FamilyOfExposedName("core_service_list"))

	t.Run("a family in instanceArg fallback exposes no family names", func(t *testing.T) {
		registerAuthFamilyMember(t, reg, "mc2-mcp-prometheus", "prometheus", "cluster")
		assert.Empty(t, reg.FamilyOfExposedName("x_prometheus_get_rules"))
		assert.Equal(t, "kubernetes", reg.FamilyOfExposedName("x_kubernetes_list_pods"), "other families are unaffected")
	})

	t.Run("the longest declared family wins when names nest", func(t *testing.T) {
		registerAuthFamilyMember(t, reg, "mc1-mcp-kubernetes-capi", "kubernetes_capi", "management_cluster")
		assert.Equal(t, "kubernetes_capi", reg.FamilyOfExposedName("x_kubernetes_capi_list_clusters"))
		assert.Equal(t, "kubernetes", reg.FamilyOfExposedName("x_kubernetes_list_pods"))
	})
}

// FamilyMembersForSession applies the same visibility rule as
// GetAllToolsForSession: an auth-protected member counts for the session when
// the session holds cached capabilities for it and the member is not down.
func TestServerRegistry_FamilyMembersForSession(t *testing.T) {
	ctx := context.Background()
	reg := NewServerRegistry("x")
	registerAuthFamilyMember(t, reg, "mc1-mcp-kubernetes", "kubernetes", "management_cluster")
	registerAuthFamilyMember(t, reg, "mc2-mcp-kubernetes", "kubernetes", "management_cluster")
	registerAuthFamilyMember(t, reg, "mc1-mcp-prometheus", "prometheus", "management_cluster")

	store := oauthstore.NewInMemoryCapabilityStore(30 * time.Minute)
	defer store.Stop()
	require.NoError(t, store.Set(ctx, "s1", "mc1-mcp-kubernetes",
		&oauthstore.Capabilities{Tools: []mcp.Tool{{Name: "list_pods", Description: "List pods"}}}))

	members, visible := reg.FamilyMembersForSession(ctx, store, "s1", "kubernetes")
	assert.Equal(t, []string{"mc1-mcp-kubernetes", "mc2-mcp-kubernetes"}, members)
	assert.Equal(t, []string{"mc1-mcp-kubernetes"}, visible, "only the member the session is connected to is visible")

	members, visible = reg.FamilyMembersForSession(ctx, store, "s2", "kubernetes")
	assert.Equal(t, []string{"mc1-mcp-kubernetes", "mc2-mcp-kubernetes"}, members)
	assert.Empty(t, visible, "a session without cached capabilities sees no member")

	members, visible = reg.FamilyMembersForSession(ctx, store, "s1", "storage")
	assert.Empty(t, members, "an undeclared family has no members")
	assert.Empty(t, visible)
}
