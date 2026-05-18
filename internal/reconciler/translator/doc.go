// Package translator turns an MCPServer spec into a backend-agnostic
// configuration model that downstream emitters (Kubernetes CRDs, agentgateway
// native YAML) serialize. Transform is pure: same input always yields the same
// Model, with no I/O. The reconciler resolves stdio shim endpoints into
// Backend.Host and Backend.Port before handing the Model to a ConfigEmitter.
package translator
