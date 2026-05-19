// Package k8s implements agentgateway.Applier against a Kubernetes API server
// via controller-runtime.
//
// One Applier is constructed per Reconcile, bound to the MCPServer's
// metav1.OwnerReference so emitted objects (AgentgatewayBackend, HTTPRoute,
// AgentgatewayPolicy) cascade-delete when the MCPServer is removed.
//
// Stdio Backends are not yet supported in cluster mode: Apply returns
// ErrStdioNotSupportedInCluster, which the reconciler surfaces as a
// NotSupportedInCluster status condition. Filesystem mode handles stdio
// natively via the yaml applier and agentgateway's mcp.targets[].stdio schema.
//
// All emitted upstream CRD types come from
// github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway —
// muster does not redeclare them.
package k8s
