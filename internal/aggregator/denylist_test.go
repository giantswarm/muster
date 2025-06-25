package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDestructiveTool(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		isDestructive bool
	}{
		{
			name:          "kubectl_apply is destructive",
			toolName:      "kubectl_apply",
			isDestructive: true,
		},
		{
			name:          "kubectl_get is not destructive",
			toolName:      "kubectl_get",
			isDestructive: false,
		},
		{
			name:          "capi_delete_cluster is destructive",
			toolName:      "capi_delete_cluster",
			isDestructive: true,
		},
		{
			name:          "get_kubernetes_resources is not destructive",
			toolName:      "get_kubernetes_resources",
			isDestructive: false,
		},
		{
			name:          "create_incident is destructive",
			toolName:      "create_incident",
			isDestructive: true,
		},
		{
			name:          "install_helm_chart is destructive",
			toolName:      "install_helm_chart",
			isDestructive: true,
		},
		{
			name:          "list_metrics is not destructive",
			toolName:      "list_metrics",
			isDestructive: false,
		},
		{
			name:          "kubectl_generic is not in the list",
			toolName:      "kubectl_generic",
			isDestructive: false,
		},
		{
			name:          "reconcile_flux_source is destructive",
			toolName:      "reconcile_flux_source",
			isDestructive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDestructiveTool(tt.toolName)
			assert.Equal(t, tt.isDestructive, result)
		})
	}
}

func TestDestructiveToolsList(t *testing.T) {
	// Verify that all the tools from the user's list are included
	expectedDestructiveTools := []string{
		"apply_kubernetes_manifest",
		"capi_create_cluster",
		"capi_create_machinedeployment",
		"capi_delete_cluster",
		"capi_delete_machine",
		"capi_move_cluster",
		"capi_pause_cluster",
		"capi_remediate_machine",
		"capi_resume_cluster",
		"capi_scale_cluster",
		"capi_scale_machinedeployment",
		"capi_update_cluster",
		"capi_upgrade_cluster",
		"cleanup",
		"create_incident",
		"delete_kubernetes_resource",
		"install_helm_chart",
		"kubectl_apply",
		"kubectl_create",
		"kubectl_delete",
		"kubectl_patch",
		"kubectl_rollout",
		"kubectl_scale",
		"reconcile_flux_helmrelease",
		"reconcile_flux_kustomization",
		"reconcile_flux_resourceset",
		"reconcile_flux_source",
		"resume_flux_reconciliation",
		"suspend_flux_reconciliation",
		"uninstall_helm_chart",
		"update_dashboard",
		"upgrade_helm_chart",
	}

	for _, toolName := range expectedDestructiveTools {
		t.Run("destructive tool: "+toolName, func(t *testing.T) {
			assert.True(t, isDestructiveTool(toolName), "Expected %s to be in destructive tools list", toolName)
		})
	}
}
