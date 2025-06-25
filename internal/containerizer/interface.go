package containerizer

import (
	"context"
	"io"
)

// ContainerRuntime defines the interface for container runtime operations
type ContainerRuntime interface {
	// PullImage pulls a container image if not already present
	PullImage(ctx context.Context, image string) error

	// StartContainer starts a container with the given configuration
	StartContainer(ctx context.Context, config ContainerConfig) (string, error)

	// StopContainer stops a running container
	StopContainer(ctx context.Context, containerID string) error

	// GetContainerLogs returns a reader for container logs
	GetContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)

	// IsContainerRunning checks if a container is running
	IsContainerRunning(ctx context.Context, containerID string) (bool, error)

	// GetContainerPort gets the mapped host port for a container port
	GetContainerPort(ctx context.Context, containerID string, containerPort string) (string, error)

	// RemoveContainer removes a container
	RemoveContainer(ctx context.Context, containerID string) error
}

// ContainerConfig holds configuration for starting a container
type ContainerConfig struct {
	Name        string            // Container name
	Image       string            // Container image
	Env         map[string]string // Environment variables
	Ports       []string          // Port mappings (host:container)
	Volumes     []string          // Volume mounts (host:container)
	Entrypoint  []string          // Entrypoint override
	User        string            // User to run as
	HealthCheck []string          // Health check command
}
