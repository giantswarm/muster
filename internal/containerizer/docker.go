package containerizer

import (
	"bufio"
	"context"
	"encoding/json"
	"muster/pkg/logging"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const dockerSubsystem = "Docker"

// DockerRuntime implements ContainerRuntime using the Docker CLI
type DockerRuntime struct {
	// We could add configuration here like docker socket path, etc.
}

// execCommandContext is a variable to allow mocking in tests
var execCommandContext = exec.CommandContext

// NewDockerRuntime creates a new Docker runtime instance
func NewDockerRuntime() (*DockerRuntime, error) {
	// Check if docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker command not found in PATH: %w", err)
	}

	// Check if docker daemon is accessible
	ctx := context.Background()
	cmd := execCommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker daemon not accessible: %w", err)
	}

	return &DockerRuntime{}, nil
}

// PullImage pulls a container image if not already present
func (d *DockerRuntime) PullImage(ctx context.Context, image string) error {
	logging.Info(dockerSubsystem, "Checking if image %s exists locally", image)

	// Check if image exists
	checkCmd := execCommandContext(ctx, "docker", "image", "inspect", image)
	if err := checkCmd.Run(); err == nil {
		logging.Debug(dockerSubsystem, "Image %s already exists", image)
		return nil
	}

	logging.Info(dockerSubsystem, "Pulling image %s", image)
	pullCmd := execCommandContext(ctx, "docker", "pull", image)
	pullCmd.Stdout = os.Stdout
	pullCmd.Stderr = os.Stderr

	if err := pullCmd.Run(); err != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, err)
	}

	return nil
}

// StartContainer starts a container with the given configuration
func (d *DockerRuntime) StartContainer(ctx context.Context, config ContainerConfig) (string, error) {
	args := []string{"run", "-d", "--name", config.Name}

	// Add environment variables
	for k, v := range config.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Add port mappings
	for _, port := range config.Ports {
		args = append(args, "-p", port)
	}

	// Add volume mounts
	for _, vol := range config.Volumes {
		// Expand tilde in volume paths
		expandedVol := expandPath(vol)
		args = append(args, "-v", expandedVol)
	}

	// Add user if specified
	if config.User != "" {
		args = append(args, "--user", config.User)
	}

	// Add entrypoint if specified
	if len(config.Entrypoint) > 0 {
		args = append(args, "--entrypoint", config.Entrypoint[0])
		if len(config.Entrypoint) > 1 {
			// Additional entrypoint args will be added after the image
		}
	}

	// Add the image
	args = append(args, config.Image)

	// Add remaining entrypoint args if any
	if len(config.Entrypoint) > 1 {
		args = append(args, config.Entrypoint[1:]...)
	}

	logging.Debug(dockerSubsystem, "Starting container with command: docker %s", strings.Join(args, " "))

	cmd := execCommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to start container: %w\nOutput: %s", err, string(output))
	}

	containerID := strings.TrimSpace(string(output))
	shortID := containerID
	if len(containerID) > 12 {
		shortID = containerID[:12]
	}
	logging.Info(dockerSubsystem, "Started container %s with ID %s", config.Name, shortID)

	return containerID, nil
}

// StopContainer stops a running container
func (d *DockerRuntime) StopContainer(ctx context.Context, containerID string) error {
	shortID := containerID
	if len(containerID) > 12 {
		shortID = containerID[:12]
	}
	logging.Info(dockerSubsystem, "Stopping container %s", shortID)

	cmd := execCommandContext(ctx, "docker", "stop", containerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop container %s: %w", shortID, err)
	}

	return nil
}

// GetContainerLogs returns a reader for container logs
func (d *DockerRuntime) GetContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	cmd := execCommandContext(ctx, "docker", "logs", "-f", containerID)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("failed to start logs command: %w", err)
	}

	// Combine stdout and stderr
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer stdout.Close()
		defer stderr.Close()

		// Read from both stdout and stderr
		go io.Copy(pw, stdout)
		io.Copy(pw, stderr)
		cmd.Wait()
	}()

	return pr, nil
}

// IsContainerRunning checks if a container is running
func (d *DockerRuntime) IsContainerRunning(ctx context.Context, containerID string) (bool, error) {
	cmd := execCommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", containerID)
	output, err := cmd.Output()
	if err != nil {
		shortID := containerID
		if len(containerID) > 12 {
			shortID = containerID[:12]
		}
		return false, fmt.Errorf("failed to inspect container %s: %w", shortID, err)
	}

	return strings.TrimSpace(string(output)) == "true", nil
}

// GetContainerPort gets the mapped host port for a container port
func (d *DockerRuntime) GetContainerPort(ctx context.Context, containerID string, containerPort string) (string, error) {
	shortID := containerID
	if len(containerID) > 12 {
		shortID = containerID[:12]
	}

	// Use docker port command to get the mapping
	cmd := execCommandContext(ctx, "docker", "port", containerID, containerPort)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get port mapping for %s:%s: %w", shortID, containerPort, err)
	}

	// Output format is usually "0.0.0.0:32768" or "[::]:32768"
	// We need to extract just the port number
	portOutput := strings.TrimSpace(string(output))
	if portOutput == "" {
		return "", fmt.Errorf("no port mapping found for %s:%s", shortID, containerPort)
	}

	// Split by colon and get the last part
	parts := strings.Split(portOutput, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected port output format: %s", portOutput)
	}

	return parts[len(parts)-1], nil
}

// RemoveContainer removes a container
func (d *DockerRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	shortID := containerID
	if len(containerID) > 12 {
		shortID = containerID[:12]
	}
	logging.Debug(dockerSubsystem, "Removing container %s", shortID)

	cmd := execCommandContext(ctx, "docker", "rm", "-f", containerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove container %s: %w", shortID, err)
	}

	return nil
}

// expandPath expands tilde in paths to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(homeDir, path[2:])
		}
	}
	return path
}

// parseContainerLogsJSON reads logs and extracts port information for MCP servers
// This is a helper that can be used to parse structured logs
func parseContainerLogsJSON(reader io.Reader) (int, error) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		// Try to parse as JSON
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
			// Look for port information in various fields
			if port, ok := logEntry["port"].(float64); ok {
				return int(port), nil
			}
			if msg, ok := logEntry["message"].(string); ok {
				// Parse port from message
				if strings.Contains(msg, "listening on port") {
					// Extract port number
					parts := strings.Fields(msg)
					for i, part := range parts {
						if part == "port" && i+1 < len(parts) {
							var port int
							if _, err := fmt.Sscanf(parts[i+1], "%d", &port); err == nil {
								return port, nil
							}
						}
					}
				}
			}
		}
	}

	return 0, fmt.Errorf("port information not found in logs")
}
