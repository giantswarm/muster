// Package v1alpha1 contains API Schema definitions for the muster v1alpha1 API group.
//
// This package defines the Kubernetes Custom Resource Definitions (CRDs) for muster's
// core components. The v1alpha1 API version represents the initial alpha release
// of the muster Kubernetes API and is subject to change.
//
// # API Group: muster.giantswarm.io/v1alpha1
//
// ## MCPServer
//
// MCPServer represents a Model Context Protocol server definition and runtime state.
// It consolidates configuration and runtime information for MCP servers that provide
// tools and capabilities to the muster system.
//
// MCPServers can be configured as local command processes with specific command
// arguments, environment variables, and lifecycle behavior.
//
// Example:
//
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: git-tools
//	  namespace: default
//	spec:
//	  name: git-tools
//	  type: localCommand
//	  autoStart: true
//	  toolPrefix: git
//	  command: ["npx", "@modelcontextprotocol/server-git"]
//	  env:
//	    GIT_ROOT: "/workspace"
//	  description: "Git tools MCP server for repository operations"
//
// +kubebuilder:object:generate=true
// +groupName=muster.giantswarm.io
package v1alpha1
