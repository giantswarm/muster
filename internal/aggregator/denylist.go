package aggregator

// destructiveTools contains the list of tools that are considered destructive
// and should be blocked by default unless --yolo flag is enabled
var destructiveTools = map[string]bool{
	// Kubernetes manifest operations
	"apply_kubernetes_manifest":  true,
	"delete_kubernetes_resource": true,
	"kubectl_apply":              true,
	"kubectl_create":             true,
	"kubectl_delete":             true,
	"kubectl_patch":              true,
	"kubectl_rollout":            true,
	"kubectl_scale":              true,

	// CAPI cluster operations
	"capi_create_cluster":           true,
	"capi_create_machinedeployment": true,
	"capi_delete_cluster":           true,
	"capi_delete_machine":           true,
	"capi_move_cluster":             true,
	"capi_pause_cluster":            true,
	"capi_remediate_machine":        true,
	"capi_resume_cluster":           true,
	"capi_scale_cluster":            true,
	"capi_scale_machinedeployment":  true,
	"capi_update_cluster":           true,
	"capi_upgrade_cluster":          true,

	// Helm operations
	"install_helm_chart":   true,
	"uninstall_helm_chart": true,
	"upgrade_helm_chart":   true,

	// Flux operations
	"reconcile_flux_helmrelease":   true,
	"reconcile_flux_kustomization": true,
	"reconcile_flux_resourceset":   true,
	"reconcile_flux_source":        true,
	"resume_flux_reconciliation":   true,
	"suspend_flux_reconciliation":  true,

	// Other destructive operations
	"cleanup":          true,
	"create_incident":  true,
	"update_dashboard": true,
}

// isDestructiveTool checks if a tool is in the destructive tools denylist
func isDestructiveTool(toolName string) bool {
	return destructiveTools[toolName]
}
