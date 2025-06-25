// Package config provides configuration management for muster.
//
// This package implements a layered configuration system that allows users to
// customize muster's behavior through YAML files. Configuration is loaded from
// multiple sources and merged in a specific order, with later sources overriding
// earlier ones.
//
// # Configuration Layers
//
// Configuration is loaded and merged in the following order:
//
//  1. Default Configuration (embedded in binary)
//     - Provides minimal defaults (no k8s connections, no MCP servers, no port forwarding)
//     - Aggregator service is enabled by default on port 8090
//     - Users must configure what they need via YAML files
//
//  2. User Configuration (~/.config/muster/config.yaml)
//     - User-specific settings that apply to all projects
//     - Useful for personal preferences and common overrides
//
//  3. Project Configuration (./.muster/config.yaml)
//     - Project-specific settings in the current directory
//     - Allows teams to share configuration via version control
//     - Note: This file is git-ignored by default
//
// # Entity Storage System
//
// The Storage system provides generic YAML-based persistence for entity definitions
// including workflows, capabilities, serviceclasses, and mcpservers. This unified
// storage system allows users to create, modify, and manage entities through both
// API operations and direct file manipulation.
//
// ## Storage Locations
//
// Entities are stored in YAML files in type-specific subdirectories:
//   - User directory: ~/.config/muster/{entityType}/
//   - Project directory: .muster/{entityType}/
//
// Where {entityType} is one of: workflows, capabilities, serviceclasses, mcpservers
//
// ## Storage Precedence
//
// The storage system follows a consistent precedence model:
//  1. Project entities override user entities with the same name
//  2. When saving, entities are saved to project directory if .muster/ exists
//  3. Otherwise, entities are saved to user directory
//
// ## Supported Operations
//
// The Storage interface provides CRUD operations:
//   - Save: Store entity data as YAML file
//   - Load: Retrieve entity data from file
//   - Delete: Remove entity file from both locations
//   - List: Get all available entity names (merged from both locations)
//
// ## File Format
//
// All entities are stored as YAML files with .yaml extension.
// Filenames are automatically sanitized to ensure filesystem compatibility.
//
// ## Usage Example
//
//	// Create storage instance
//	storage := config.NewStorage()
//
//	// Save a workflow
//	workflowYAML := []byte(`name: "my-workflow"
//	description: "Example workflow"
//	steps: []`)
//	err := storage.Save("workflows", "my-workflow", workflowYAML)
//
//	// Load the workflow
//	data, err := storage.Load("workflows", "my-workflow")
//
//	// List all workflows
//	names, err := storage.List("workflows")
//
//	// Delete the workflow
//	err = storage.Delete("workflows", "my-workflow")
//
// # Configuration Structure
//
// The configuration file uses YAML format with the following main sections:
//
//	clusters:
//	  - name: "mc-example"
//	    context: "gs-example"
//	    role: "observability"  # or "target", "custom"
//	    displayName: "MC: example"
//	    icon: "🏢"
//
//	activeClusters:
//	  observability: "mc-example"
//	  target: "wc-myworkload"
//
//	portForwards:
//	  - name: "mc-prometheus"
//	    enabledByDefault: true
//	    clusterRole: "observability"  # or "target", or use clusterName
//	    namespace: "mimir"
//	    targetType: "service"  # or "pod", "deployment", "statefulset"
//	    targetName: "mimir-query-frontend"
//	    localPort: "8080"
//	    remotePort: "8080"
//
//	mcpServers:
//	  - name: "kubernetes"
//	    type: "localCommand"  # or "container"
//	    enabledByDefault: true
//	    command: ["npx", "mcp-server-kubernetes"]
//	    requiresClusterRole: "target"
//	    env:
//	      KEY: "value"
//
// # Cluster Configuration
//
// Clusters define available Kubernetes contexts and their roles:
//
//   - name: Unique identifier for the cluster
//   - context: Kubernetes context name from kubeconfig
//   - role: The cluster's purpose (observability, target, custom)
//   - displayName: Human-friendly name for UI display
//   - icon: Optional emoji/icon for UI
//
// # Port Forward Configuration
//
// Port forwards define kubectl port-forward tunnels to expose cluster services locally:
//
//   - name: Unique identifier for the port forward
//   - enabledByDefault: Whether this port forward should be started automatically
//   - clusterRole: Which cluster role to use (references activeClusters)
//   - clusterName: Specific cluster name (overrides clusterRole)
//   - namespace: Kubernetes namespace containing the target
//   - targetType: Type of Kubernetes resource (service, pod, deployment, statefulset)
//   - targetName: Name of the Kubernetes resource
//   - localPort: Local port to bind to
//   - remotePort: Remote port on the target
//
// # MCP Server Configuration
//
// MCP servers can be configured as local commands or Docker containers:
//
// Local Command:
//   - type: "localCommand"
//   - command: Array of command and arguments
//   - env: Environment variables to set
//   - requiresClusterRole: Which cluster role this server needs
//   - requiresPortForwards: Array of port forward names this server depends on
//
// Container:
//   - type: "container"
//   - image: Docker image to use
//   - containerPorts: Port mappings (host:container format)
//   - containerVolumes: Volume mounts
//   - containerEnv: Environment variables
//
// # Environment Variable Expansion
//
// Configuration values support environment variable expansion:
//
//	env:
//	  API_KEY: "${MY_API_KEY}"
//	  HOME_DIR: "${HOME}/data"
//	  WITH_DEFAULT: "${MISSING:-default_value}"
//
// # Usage Example
//
//	// Load configuration (returns minimal defaults if no config files exist)
//	cfg, err := config.LoadConfig("", "")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Access cluster configurations
//	for _, cluster := range cfg.Clusters {
//	    fmt.Printf("Cluster %s: %s (role: %s)\n",
//	        cluster.Name, cluster.Context, cluster.Role)
//	}
//
//	// Access MCP server configurations
//	for _, mcp := range cfg.MCPServers {
//	    if mcp.Enabled {
//	        fmt.Printf("MCP server %s: %v\n",
//	            mcp.Name, mcp.Command)
//	    }
//	}
//
// # Active Clusters
//
// The active clusters mapping specifies which cluster is currently active for each role:
//   - One cluster per role can be active at a time
//   - Services will use the active cluster for their configured role
//   - Active clusters can be changed at runtime through the TUI or API
//
// # Configuration API
//
// The configuration can be accessed and modified at runtime through the Configuration API.
// The API adapter (ConfigAdapter) is located in the app package rather than here to avoid
// circular import dependencies, as the adapter needs to import the api package for registration,
// while the api package imports this package for type definitions.
package config
