// Package containerizer provides container runtime abstraction for muster.
//
// This package handles the complexity of working with different container
// runtimes (Docker, Podman, etc.) through a common interface. It is primarily
// used for running containerized MCP servers.
//
// # Core Components
//
// ContainerRuntime: Interface that abstracts container operations
//   - PullImage: Download container images
//   - CreateContainer: Create new containers with configuration
//   - StartContainer: Start created containers
//   - StopContainer: Stop running containers
//   - RemoveContainer: Clean up containers
//   - GetContainerLogs: Stream container output
//
// DockerRuntime: Implementation for Docker container runtime
//   - Uses Docker API client
//   - Handles Docker-specific configurations
//   - Manages container lifecycle
//
// # Container Configuration
//
// Containers are configured with:
//   - Image: Container image to run
//   - Ports: Port mappings between host and container
//   - Environment: Environment variables
//   - Volumes: Volume mounts for persistent data
//   - User: User/group to run container as
//   - Network: Network mode (host, bridge, etc.)
//
// # Usage Example
//
//	// Create runtime based on configuration
//	runtime, err := containerizer.NewRuntime("docker")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Pull image if needed
//	err = runtime.PullImage(ctx, "ghcr.io/org/mcp-server:latest")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create and start container
//	config := ContainerConfig{
//	    Image: "ghcr.io/org/mcp-server:latest",
//	    Ports: []string{"8080:8080"},
//	    Env: map[string]string{
//	        "MCP_PORT": "8080",
//	    },
//	}
//	containerID, err := runtime.CreateContainer(ctx, "mcp-server", config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	err = runtime.StartContainer(ctx, containerID)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// # Error Handling
//
// The package handles various container-related errors:
//   - Image not found
//   - Container already exists
//   - Port conflicts
//   - Volume mount issues
//   - Runtime not available
//
// # Thread Safety
//
// All runtime implementations are thread-safe and can be used
// concurrently from multiple goroutines.
package containerizer
