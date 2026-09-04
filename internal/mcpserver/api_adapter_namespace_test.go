package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// TestConvertCRDToInfo_CarriesTheResourceNamespace guards the first hop of the
// MCPServer's namespace: from the resource's metadata into the info the
// orchestrator turns into the service definition. Everything downstream — the
// aggregator's registry entry, the default namespace for the definition's
// Secret references, the namespace its events are filed under — reads it from
// there, and an empty value silently means "default".
func TestConvertCRDToInfo_CarriesTheResourceNamespace(t *testing.T) {
	info := convertCRDToInfo(&musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "agent-platform"},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https://api.githubcopilot.com/mcp/"},
	})
	assert.Equal(t, "agent-platform", info.Namespace)
	assert.Equal(t, "agent-platform", info.ToMCPServer().Namespace, "the service definition keeps the namespace")

	local := convertCRDToInfo(&musterv1alpha1.MCPServer{ObjectMeta: metav1.ObjectMeta{Name: "local"}})
	assert.Empty(t, local.Namespace, "filesystem mode has no namespace")
	assert.Empty(t, local.ToMCPServer().Namespace)
}
