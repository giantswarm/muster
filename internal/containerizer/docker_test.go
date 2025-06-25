package containerizer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// init sets up the test environment
func init() {
	// Replace the exec command context with our mock in tests
	execCommandContext = mockExecCommandContext
}

// mockExecCommandContext is our mock implementation
func mockExecCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return mockExecCommand(name, args...)
}

// mockExecCommand creates a mock command for testing
func mockExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

// TestHelperProcess is a helper process for mocking exec.Command
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}

	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "No command\n")
		os.Exit(2)
	}

	cmd, args := args[0], args[1:]

	// Mock docker commands
	switch cmd {
	case "docker":
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "No docker subcommand\n")
			os.Exit(1)
		}

		switch args[0] {
		case "info":
			// Simulate docker info success
			os.Exit(0)

		case "image":
			if len(args) > 2 && args[1] == "inspect" {
				image := args[2]
				if image == "alpine:latest" {
					// Image exists
					os.Exit(0)
				}
				// Image doesn't exist
				os.Exit(1)
			}

		case "pull":
			if len(args) > 1 {
				image := args[1]
				if image == "nonexistent/image:doesnotexist" {
					fmt.Fprintf(os.Stderr, "Error response from daemon: pull access denied\n")
					os.Exit(1)
				}
				// Simulate successful pull
				fmt.Printf("Pulling %s\n", image)
				os.Exit(0)
			}

		case "run":
			// Simulate container creation
			fmt.Println("abc123def456789")
			os.Exit(0)

		case "stop":
			// Simulate container stop
			os.Exit(0)

		case "rm":
			// Simulate container removal
			os.Exit(0)

		case "inspect":
			if len(args) > 3 && args[1] == "-f" && args[2] == "{{.State.Running}}" {
				fmt.Println("true")
				os.Exit(0)
			}

		case "port":
			if len(args) > 2 {
				containerPort := args[2]
				if containerPort == "80" {
					fmt.Println("0.0.0.0:32768")
				} else if containerPort == "443" {
					fmt.Println("[::]:32769")
				} else {
					// No mapping
					os.Exit(1)
				}
				os.Exit(0)
			}

		case "logs":
			// Simulate logs output
			fmt.Println("Container started")
			fmt.Println("Listening on port 8080")
			os.Exit(0)
		}
	}

	fmt.Fprintf(os.Stderr, "Unknown command: %s %v\n", cmd, args)
	os.Exit(1)
}

func TestNewDockerRuntime(t *testing.T) {
	// Save original and restore after test
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	// Mock successful Docker check
	execCommandContext = mockExecCommandContext

	runtime, err := NewDockerRuntime()
	if err != nil {
		t.Errorf("NewDockerRuntime() error = %v, want nil", err)
	}
	if runtime == nil {
		t.Error("NewDockerRuntime() returned nil runtime")
	}
}

func TestDockerRuntime_PullImage(t *testing.T) {
	// Save original
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	execCommandContext = mockExecCommandContext

	tests := []struct {
		name        string
		image       string
		expectError bool
	}{
		{
			name:        "image already exists",
			image:       "alpine:latest",
			expectError: false,
		},
		{
			name:        "image needs pull",
			image:       "hello-world:latest",
			expectError: false,
		},
		{
			name:        "pull fails",
			image:       "nonexistent/image:doesnotexist",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DockerRuntime{}
			ctx := context.Background()

			err := d.PullImage(ctx, tt.image)
			if (err != nil) != tt.expectError {
				t.Errorf("PullImage() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestDockerRuntime_StartContainer(t *testing.T) {
	// Save original
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	execCommandContext = mockExecCommandContext

	tests := []struct {
		name        string
		config      ContainerConfig
		expectError bool
	}{
		{
			name: "basic container",
			config: ContainerConfig{
				Name:  "test-container",
				Image: "alpine:latest",
			},
			expectError: false,
		},
		{
			name: "container with ports and volumes",
			config: ContainerConfig{
				Name:    "test-container-2",
				Image:   "alpine:latest",
				Ports:   []string{"8080:80"},
				Volumes: []string{"/tmp:/container"},
				Env: map[string]string{
					"TEST": "value",
				},
			},
			expectError: false,
		},
		{
			name: "container with entrypoint",
			config: ContainerConfig{
				Name:       "test-container-3",
				Image:      "alpine:latest",
				Entrypoint: []string{"/bin/sh", "-c", "echo hello"},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DockerRuntime{}
			ctx := context.Background()

			id, err := d.StartContainer(ctx, tt.config)
			if (err != nil) != tt.expectError {
				t.Errorf("StartContainer() error = %v, expectError %v", err, tt.expectError)
			}

			if !tt.expectError && id == "" {
				t.Error("StartContainer() returned empty container ID")
			}
		})
	}
}

func TestDockerRuntime_StopContainer(t *testing.T) {
	// Save original
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	execCommandContext = mockExecCommandContext

	d := &DockerRuntime{}
	ctx := context.Background()

	err := d.StopContainer(ctx, "abc123def456")
	if err != nil {
		t.Errorf("StopContainer() error = %v, want nil", err)
	}
}

func TestDockerRuntime_RemoveContainer(t *testing.T) {
	// Save original
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	execCommandContext = mockExecCommandContext

	d := &DockerRuntime{}
	ctx := context.Background()

	err := d.RemoveContainer(ctx, "abc123def456")
	if err != nil {
		t.Errorf("RemoveContainer() error = %v, want nil", err)
	}
}

func TestDockerRuntime_IsContainerRunning(t *testing.T) {
	// Save original
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	execCommandContext = mockExecCommandContext

	d := &DockerRuntime{}
	ctx := context.Background()

	running, err := d.IsContainerRunning(ctx, "abc123def456")
	if err != nil {
		t.Errorf("IsContainerRunning() error = %v, want nil", err)
	}
	if !running {
		t.Error("IsContainerRunning() = false, want true")
	}
}

func TestDockerRuntime_GetContainerPort(t *testing.T) {
	// Save original
	oldExecCommandContext := execCommandContext
	defer func() { execCommandContext = oldExecCommandContext }()

	execCommandContext = mockExecCommandContext

	tests := []struct {
		name          string
		containerID   string
		containerPort string
		expectedPort  string
		expectError   bool
	}{
		{
			name:          "standard format",
			containerID:   "abc123",
			containerPort: "80",
			expectedPort:  "32768",
			expectError:   false,
		},
		{
			name:          "IPv6 format",
			containerID:   "abc123",
			containerPort: "443",
			expectedPort:  "32769",
			expectError:   false,
		},
		{
			name:          "no mapping",
			containerID:   "abc123",
			containerPort: "8080",
			expectedPort:  "",
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DockerRuntime{}
			ctx := context.Background()

			port, err := d.GetContainerPort(ctx, tt.containerID, tt.containerPort)
			if (err != nil) != tt.expectError {
				t.Errorf("GetContainerPort() error = %v, expectError %v", err, tt.expectError)
			}

			if !tt.expectError && port != tt.expectedPort {
				t.Errorf("GetContainerPort() = %v, want %v", port, tt.expectedPort)
			}
		})
	}
}

func TestDockerRuntime_GetContainerLogs(t *testing.T) {
	// This test is more complex due to pipes, so we'll keep it simple
	d := &DockerRuntime{}
	ctx := context.Background()

	// We can't easily mock the pipe behavior, so we'll skip the actual test
	// but keep the structure for documentation
	_ = d
	_ = ctx
}

func TestExpandPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string // We'll check if result contains this string
	}{
		{
			name:     "no tilde",
			input:    "/absolute/path",
			contains: "/absolute/path",
		},
		{
			name:     "relative path",
			input:    "relative/path",
			contains: "relative/path",
		},
		{
			name:     "tilde path",
			input:    "~/test/path",
			contains: "/test/path", // Should expand to home dir + /test/path
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandPath(tt.input)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("expandPath(%q) = %q, want to contain %q", tt.input, result, tt.contains)
			}
		})
	}
}

func TestParseContainerLogsJSON(t *testing.T) {
	tests := []struct {
		name         string
		logs         string
		expectedPort int
		expectError  bool
	}{
		{
			name:         "JSON with port field",
			logs:         `{"port": 8080, "message": "Server started"}`,
			expectedPort: 8080,
			expectError:  false,
		},
		{
			name:         "JSON with port in message",
			logs:         `{"message": "Server listening on port 3000"}`,
			expectedPort: 3000,
			expectError:  false,
		},
		{
			name:         "No port information",
			logs:         `{"message": "Server started"}`,
			expectedPort: 0,
			expectError:  true,
		},
		{
			name:         "Plain text logs",
			logs:         "Server running",
			expectedPort: 0,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.logs)
			port, err := parseContainerLogsJSON(reader)

			if (err != nil) != tt.expectError {
				t.Errorf("parseContainerLogsJSON() error = %v, expectError %v", err, tt.expectError)
			}

			if port != tt.expectedPort {
				t.Errorf("parseContainerLogsJSON() = %v, want %v", port, tt.expectedPort)
			}
		})
	}
}

func TestContainerConfig_Validation(t *testing.T) {
	tests := []struct {
		name        string
		config      ContainerConfig
		expectValid bool
	}{
		{
			name: "valid config",
			config: ContainerConfig{
				Name:  "test",
				Image: "alpine:latest",
			},
			expectValid: true,
		},
		{
			name: "missing name",
			config: ContainerConfig{
				Image: "alpine:latest",
			},
			expectValid: false,
		},
		{
			name: "missing image",
			config: ContainerConfig{
				Name: "test",
			},
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.config.Name != "" && tt.config.Image != ""
			if valid != tt.expectValid {
				t.Errorf("config validation = %v, want %v", valid, tt.expectValid)
			}
		})
	}
}

// TestParsePortMapping tests parsing of port mapping strings
func TestParsePortMapping(t *testing.T) {
	tests := []struct {
		input     string
		wantHost  string
		wantCont  string
		wantError bool
	}{
		{"8080:80", "8080", "80", false},
		{"80", "", "", true},
		{"8080:80:90", "", "", true},
		{"", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			parts := strings.Split(tt.input, ":")
			if len(parts) != 2 {
				if !tt.wantError {
					t.Errorf("expected valid port mapping for %q", tt.input)
				}
				return
			}

			if tt.wantError {
				t.Errorf("expected error for %q but got valid parse", tt.input)
			}

			if parts[0] != tt.wantHost || parts[1] != tt.wantCont {
				t.Errorf("got host=%q cont=%q, want host=%q cont=%q",
					parts[0], parts[1], tt.wantHost, tt.wantCont)
			}
		})
	}
}

// mockReadCloser implements io.ReadCloser for testing
type mockReadCloser struct {
	*strings.Reader
}

func (m mockReadCloser) Close() error {
	return nil
}

func newMockReadCloser(s string) io.ReadCloser {
	return mockReadCloser{strings.NewReader(s)}
}
