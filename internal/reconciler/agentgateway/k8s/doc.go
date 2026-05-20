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
//
// # Ownership
//
// Muster owns the entire Spec of every object it emits — AgentgatewayBackend,
// HTTPRoute, AgentgatewayPolicy. Each reconcile rewrites Spec wholesale from
// the MCPServer-derived agentgateway.Config; external edits to Spec are
// reverted on the next Apply. Consumers that need additional Gateway API
// filters or parent refs must request them via an MCPServer field rather
// than mutating the HTTPRoute directly.
package k8s
