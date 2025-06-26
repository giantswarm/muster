package aggregator

// destructiveTools defines a security denylist of tools that are considered
// potentially destructive and should be blocked by default in production environments.
//
// This denylist implements a "secure by default" approach, where tools that can
// modify system state, delete resources, or perform irreversible operations are
// blocked unless explicitly enabled via the --yolo flag.
//
// The denylist is categorized by operation type for better maintainability:
//
// Kubernetes Operations:
//   - Manifest application and resource deletion
//   - kubectl commands that modify cluster state
//
// CAPI (Cluster API) Operations:
//   - Cluster lifecycle management (create, delete, scale)
//   - Machine and deployment operations
//   - Cluster maintenance operations
//
// Helm Operations:
//   - Chart installation, upgrades, and uninstallation
//
// Flux Operations:
//   - GitOps reconciliation and resource management
//   - Source and HelmRelease operations
//
// General Operations:
//   - System cleanup and maintenance
//   - Incident management
//   - Dashboard modifications
//
// Security Philosophy:
// Better to err on the side of caution and require explicit opt-in for
// destructive operations rather than risk accidental system damage.
var destructiveTools = map[string]bool{
	// Kubernetes manifest operations - directly modify cluster state
	"apply_kubernetes_manifest":  true, // Applies manifests that can create/modify resources
	"delete_kubernetes_resource": true, // Permanently removes Kubernetes resources
	"kubectl_apply":              true, // Applies configuration changes to cluster
	"kubectl_create":             true, // Creates new Kubernetes resources
	"kubectl_delete":             true, // Permanently deletes Kubernetes resources
	"kubectl_patch":              true, // Modifies existing Kubernetes resources
	"kubectl_rollout":            true, // Manages deployment rollouts (can cause downtime)
	"kubectl_scale":              true, // Changes resource scaling (affects availability)

	// CAPI (Cluster API) cluster operations - manage entire cluster lifecycles
	"capi_create_cluster":           true, // Creates new Kubernetes clusters (expensive)
	"capi_create_machinedeployment": true, // Creates new compute resources
	"capi_delete_cluster":           true, // Permanently destroys entire clusters
	"capi_delete_machine":           true, // Removes compute nodes (affects capacity)
	"capi_move_cluster":             true, // Migrates cluster management (risky operation)
	"capi_pause_cluster":            true, // Stops cluster operations (affects availability)
	"capi_remediate_machine":        true, // Replaces problematic machines (affects capacity)
	"capi_resume_cluster":           true, // Resumes cluster operations (state change)
	"capi_scale_cluster":            true, // Changes cluster size (affects costs/capacity)
	"capi_scale_machinedeployment":  true, // Changes compute capacity
	"capi_update_cluster":           true, // Modifies cluster configuration (risky)
	"capi_upgrade_cluster":          true, // Upgrades cluster version (potential downtime)

	// Helm operations - package management with potential system impact
	"install_helm_chart":   true, // Installs new applications (can affect system)
	"uninstall_helm_chart": true, // Removes applications and their data
	"upgrade_helm_chart":   true, // Updates applications (potential breaking changes)

	// Flux operations - GitOps operations that trigger deployments
	"reconcile_flux_helmrelease":   true, // Forces application deployments
	"reconcile_flux_kustomization": true, // Forces configuration deployments
	"reconcile_flux_resourceset":   true, // Forces resource deployments
	"reconcile_flux_source":        true, // Forces source synchronization
	"resume_flux_reconciliation":   true, // Resumes automated deployments
	"suspend_flux_reconciliation":  true, // Stops automated deployments

	// Other destructive operations - general system maintenance
	"cleanup":          true, // System cleanup operations (may delete data)
	"create_incident":  true, // Creates incidents in monitoring systems
	"update_dashboard": true, // Modifies monitoring/observability dashboards
}

// isDestructiveTool checks whether a given tool is classified as destructive.
//
// This function implements the core security check that determines whether
// a tool should be blocked by the denylist. It performs a simple lookup
// in the destructiveTools map.
//
// The check is performed using the original tool name (before any prefixing)
// to ensure consistent behavior regardless of how the tool is exposed.
//
// Parameters:
//   - toolName: The original tool name (without prefixes) to check
//
// Returns:
//   - true if the tool is considered destructive and should be blocked
//   - false if the tool is safe to execute
//
// Usage:
// This function is called before executing any tool when the aggregator
// is running in secure mode (yolo=false). If the tool is destructive,
// the execution is blocked with an appropriate error message.
func isDestructiveTool(toolName string) bool {
	return destructiveTools[toolName]
}
