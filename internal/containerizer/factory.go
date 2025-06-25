package containerizer

import (
	"fmt"
	"strings"
)

// RuntimeType defines the type of container runtime
type RuntimeType string

const (
	RuntimeTypeDocker RuntimeType = "docker"
	RuntimeTypePodman RuntimeType = "podman"
)

// NewContainerRuntime creates a new container runtime based on the specified type
func NewContainerRuntime(runtimeType string) (ContainerRuntime, error) {
	rt := RuntimeType(strings.ToLower(runtimeType))

	switch rt {
	case RuntimeTypeDocker, "":
		// Default to Docker if not specified
		return NewDockerRuntime()
	case RuntimeTypePodman:
		// TODO: Implement Podman runtime
		return nil, fmt.Errorf("podman runtime not yet implemented")
	default:
		return nil, fmt.Errorf("unsupported container runtime: %s", runtimeType)
	}
}
