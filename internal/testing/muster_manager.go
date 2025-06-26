package testing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// logCapture captures stdout and stderr from a process
type logCapture struct {
	stdoutBuf    *bytes.Buffer
	stderrBuf    *bytes.Buffer
	stdoutReader *io.PipeReader
	stderrReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrWriter *io.PipeWriter
	wg           sync.WaitGroup
	mu           sync.RWMutex
}

// newLogCapture creates a new log capture instance
func newLogCapture() *logCapture {
	lc := &logCapture{
		stdoutBuf: &bytes.Buffer{},
		stderrBuf: &bytes.Buffer{},
	}

	lc.stdoutReader, lc.stdoutWriter = io.Pipe()
	lc.stderrReader, lc.stderrWriter = io.Pipe()

	// Start goroutines to capture output
	lc.wg.Add(2)
	go lc.captureOutput(lc.stdoutReader, lc.stdoutBuf)
	go lc.captureOutput(lc.stderrReader, lc.stderrBuf)

	return lc
}

// captureOutput captures output from a reader to a buffer
func (lc *logCapture) captureOutput(reader io.Reader, buffer *bytes.Buffer) {
	defer lc.wg.Done()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		lc.mu.Lock()
		buffer.WriteString(line + "\n")
		lc.mu.Unlock()
	}
}

// close closes the capture pipes and waits for completion
func (lc *logCapture) close() {
	lc.stdoutWriter.Close()
	lc.stderrWriter.Close()
	lc.wg.Wait()
}

// getLogs returns the captured logs
func (lc *logCapture) getLogs() *InstanceLogs {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	stdout := lc.stdoutBuf.String()
	stderr := lc.stderrBuf.String()

	// Create combined log with simple interleaving
	combined := ""
	if stdout != "" {
		combined += "=== STDOUT ===\n" + stdout
	}
	if stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += "=== STDERR ===\n" + stderr
	}

	return &InstanceLogs{
		Stdout:   stdout,
		Stderr:   stderr,
		Combined: combined,
	}
}

// managedProcess represents a managed muster process with its command and log capture
type managedProcess struct {
	cmd        *exec.Cmd
	logCapture *logCapture
}

// musterInstanceManager implements the MusterInstanceManager interface
type musterInstanceManager struct {
	debug      bool
	basePort   int
	portOffset int
	tempDir    string
	processes  map[string]*managedProcess // Track processes by instance ID
	mu         sync.RWMutex
	logger     TestLogger

	// Port reservation system for thread-safe parallel execution
	portMu        sync.Mutex     // Protects port allocation
	reservedPorts map[int]string // port -> instanceID mapping
}

// NewMusterInstanceManager creates a new muster instance manager
func NewMusterInstanceManager(debug bool, basePort int) (MusterInstanceManager, error) {
	return NewMusterInstanceManagerWithLogger(debug, basePort, NewStdoutLogger(false, debug))
}

// NewMusterInstanceManagerWithLogger creates a new muster instance manager with custom logger
func NewMusterInstanceManagerWithLogger(debug bool, basePort int, logger TestLogger) (MusterInstanceManager, error) {
	// Create temporary directory for test configurations
	tempDir, err := os.MkdirTemp("", "muster-test-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &musterInstanceManager{
		debug:         debug,
		basePort:      basePort,
		tempDir:       tempDir,
		processes:     make(map[string]*managedProcess),
		logger:        logger,
		reservedPorts: make(map[int]string),
	}, nil
}

// CreateInstance creates a new muster serve instance with the given configuration
func (m *musterInstanceManager) CreateInstance(ctx context.Context, scenarioName string, config *MusterPreConfiguration) (*MusterInstance, error) {
	// Generate unique instance ID
	instanceID := fmt.Sprintf("test-%s-%d", sanitizeFileName(scenarioName), time.Now().UnixNano())

	// Find available port (with atomic reservation)
	port, err := m.findAvailablePort(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to find available port: %w", err)
	}

	// Create instance configuration directory
	configPath := filepath.Join(m.tempDir, instanceID)
	if err := os.MkdirAll(configPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	if m.debug {
		m.logger.Debug("🏗️  Creating muster instance %s with config at %s\n", instanceID, configPath)
	}

	// Generate configuration files
	if err := m.generateConfigFiles(configPath, config, port); err != nil {
		return nil, fmt.Errorf("failed to generate config files: %w", err)
	}

	// Start muster serve process with log capture
	managedProc, err := m.startMusterProcess(ctx, configPath, port)
	if err != nil {
		// Clean up on failure: release port and remove config directory
		m.releasePort(port, instanceID)
		os.RemoveAll(configPath)
		return nil, fmt.Errorf("failed to start muster process: %w", err)
	}

	// Store the managed process
	m.mu.Lock()
	m.processes[instanceID] = managedProc
	m.mu.Unlock()

	// Extract expected resources from configuration
	expectedTools := m.extractExpectedTools(config)
	expectedServiceClasses := m.extractExpectedServiceClasses(config)

	instance := &MusterInstance{
		ID:                     instanceID,
		ConfigPath:             configPath,
		Port:                   port,
		Endpoint:               fmt.Sprintf("http://localhost:%d/mcp", port),
		Process:                managedProc.cmd.Process,
		StartTime:              time.Now(),
		Logs:                   nil, // Will be populated when destroying
		ExpectedTools:          expectedTools,
		ExpectedServiceClasses: expectedServiceClasses,
	}

	if m.debug {
		m.logger.Debug("🚀 Started muster instance %s on port %d (PID: %d)\n", instanceID, port, managedProc.cmd.Process.Pid)
	}

	return instance, nil
}

// DestroyInstance stops and cleans up an muster serve instance
func (m *musterInstanceManager) DestroyInstance(ctx context.Context, instance *MusterInstance) error {
	if m.debug {
		m.logger.Debug("🛑 Destroying muster instance %s (PID: %d)\n", instance.ID, instance.Process.Pid)
	}

	// Get the managed process
	m.mu.RLock()
	managedProc, exists := m.processes[instance.ID]
	m.mu.RUnlock()

	if exists && managedProc != nil {
		// Attempt graceful shutdown first
		if err := m.gracefulShutdown(managedProc, instance.ID); err != nil {
			if m.debug {
				m.logger.Debug("⚠️  Graceful shutdown failed for %s: %v, forcing termination\n", instance.ID, err)
			}
		}

		// Collect logs before cleanup
		if managedProc.logCapture != nil {
			managedProc.logCapture.close()
			instance.Logs = managedProc.logCapture.getLogs()
		}

		// Clean up from processes map
		m.mu.Lock()
		delete(m.processes, instance.ID)
		m.mu.Unlock()
	}

	// Release the reserved port
	m.releasePort(instance.Port, instance.ID)

	// Clean up configuration directory
	if err := os.RemoveAll(instance.ConfigPath); err != nil {
		if m.debug {
			m.logger.Debug("⚠️  Failed to remove config directory %s: %v\n", instance.ConfigPath, err)
		}
		return fmt.Errorf("failed to remove config directory: %w", err)
	}

	if m.debug {
		m.logger.Debug("✅ Destroyed muster instance %s\n", instance.ID)
	}

	return nil
}

// gracefulShutdown attempts to gracefully shutdown an muster process and all its children
func (m *musterInstanceManager) gracefulShutdown(managedProc *managedProcess, instanceID string) error {
	if managedProc.cmd == nil || managedProc.cmd.Process == nil {
		return fmt.Errorf("no process to shutdown")
	}

	process := managedProc.cmd.Process

	if m.debug {
		m.logger.Debug("🛑 Shutting down process group for %s (PID: %d)\n", instanceID, process.Pid)
	}

	// First, send SIGTERM to the entire process group to terminate all children
	if err := m.killProcessGroup(process.Pid, syscall.SIGTERM); err != nil {
		if m.debug {
			m.logger.Debug("⚠️  Failed to send SIGTERM to process group %d: %v\n", process.Pid, err)
		}
	}

	// Wait for graceful shutdown with timeout
	shutdownTimeout := 10 * time.Second
	done := make(chan error, 1)

	go func() {
		err := managedProc.cmd.Wait()
		done <- err
	}()

	select {
	case err := <-done:
		if m.debug {
			if err != nil {
				m.logger.Debug("✅ Process %s exited with: %v\n", instanceID, err)
			} else {
				m.logger.Debug("✅ Process %s exited gracefully\n", instanceID)
			}
		}
		// Ensure any remaining child processes are killed
		m.killProcessGroup(process.Pid, syscall.SIGKILL)
		return nil
	case <-time.After(shutdownTimeout):
		if m.debug {
			m.logger.Debug("⏰ Graceful shutdown timeout for %s, forcing kill of entire process group\n", instanceID)
		}
		// Force kill the entire process group
		return m.killProcessGroup(process.Pid, syscall.SIGKILL)
	}
}



// WaitForReady waits for an instance to be ready to accept connections and has all expected resources available
func (m *musterInstanceManager) WaitForReady(ctx context.Context, instance *MusterInstance) error {
	if m.debug {
		m.logger.Debug("⏳ Waiting for muster instance %s to be ready at %s\n", instance.ID, instance.Endpoint)
	}

	timeout := 60 * time.Second // Increased timeout for more complex setups
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Give the process a moment to start
	time.Sleep(2 * time.Second)

	// First wait for port to be available
	portReady := false
	for !portReady {
		select {
		case <-readyCtx.Done():
			if m.debug {
				m.showLogs(instance)
			}
			return fmt.Errorf("timeout waiting for muster instance port to be ready")
		case <-ticker.C:
			// Check if port is accepting connections
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", instance.Port), 1*time.Second)
			if err == nil {
				conn.Close()
				portReady = true
				if m.debug {
					m.logger.Debug("✅ Port %d is ready\n", instance.Port)
				}
			} else if m.debug {
				m.logger.Debug("🔍 Port %d not ready yet: %v\n", instance.Port, err)
			}
		}
	}

	// Now wait for services to be fully initialized
	if m.debug {
		m.logger.Debug("⏳ Waiting for services to be fully initialized and all resources to be available...\n")
	}

	// Create MCP client to check availability
	mcpClient := NewMCPTestClient(m.debug)
	defer mcpClient.Close()

	// Connect to the MCP aggregator
	connectCtx, connectCancel := context.WithTimeout(readyCtx, 30*time.Second)
	defer connectCancel()

	// Retry connection until successful or timeout
	var connected bool
	for !connected {
		select {
		case <-connectCtx.Done():
			if m.debug {
				m.logger.Debug("⚠️  Failed to connect to MCP aggregator, proceeding anyway\n")
			}
			// If we can't connect to MCP, fall back to the old behavior
			time.Sleep(3 * time.Second)
			return nil
		case <-time.After(1 * time.Second):
			err := mcpClient.Connect(connectCtx, instance.Endpoint)
			if err == nil {
				connected = true
				if m.debug {
					m.logger.Debug("✅ Connected to MCP aggregator\n")
				}
			} else if m.debug {
				m.logger.Debug("🔍 Waiting for MCP connection: %v\n", err)
			}
		}
	}

	// Extract expected resources from the pre-configuration
	expectedTools := m.extractExpectedToolsFromInstance(instance)
	expectedServiceClasses := m.extractExpectedServiceClassesFromInstance(instance)
	expectedServices := m.extractExpectedServicesFromInstance(instance)
	expectedWorkflows := m.extractExpectedWorkflowsFromInstance(instance)
	expectedCapabilities := m.extractExpectedCapabilitiesFromInstance(instance)

	if len(expectedTools) == 0 && len(expectedServiceClasses) == 0 && len(expectedServices) == 0 &&
		len(expectedWorkflows) == 0 && len(expectedCapabilities) == 0 {
		if m.debug {
			m.logger.Debug("ℹ️  No expected resources specified, waiting for basic service readiness\n")
		}
		// If no specific resources expected, wait a bit longer for general readiness
		time.Sleep(5 * time.Second)
		return nil
	}

	if m.debug {
		if len(expectedTools) > 0 {
			m.logger.Debug("🎯 Waiting for %d expected tools: %v\n", len(expectedTools), expectedTools)
		}
		if len(expectedServiceClasses) > 0 {
			m.logger.Debug("🎯 Waiting for %d expected ServiceClasses: %v\n", len(expectedServiceClasses), expectedServiceClasses)
		}
		if len(expectedServices) > 0 {
			m.logger.Debug("🎯 Waiting for %d expected Services: %v\n", len(expectedServices), expectedServices)
		}
		if len(expectedWorkflows) > 0 {
			m.logger.Debug("🎯 Waiting for %d expected Workflows: %v\n", len(expectedWorkflows), expectedWorkflows)
		}
		if len(expectedCapabilities) > 0 {
			m.logger.Debug("🎯 Waiting for %d expected Capabilities: %v\n", len(expectedCapabilities), expectedCapabilities)
		}
	}

	// Wait for all expected resources to be available
	resourceTimeout := 5 * time.Second
	resourceCtx, resourceCancel := context.WithTimeout(readyCtx, resourceTimeout)
	defer resourceCancel()

	resourceTicker := time.NewTicker(2 * time.Second)
	defer resourceTicker.Stop()

	for {
		select {
		case <-resourceCtx.Done():
			if m.debug {
				m.logger.Debug("⚠️  Resource availability check timed out, checking what's available...\n")
				// Show what's available for debugging
				if len(expectedTools) > 0 {
					if availableTools, err := mcpClient.ListTools(context.Background()); err == nil {
						m.logger.Debug("🛠️  Available tools: %v\n", availableTools)
						m.logger.Debug("🎯 Expected tools: %v\n", expectedTools)
					}
				}
			}
			return fmt.Errorf("timeout waiting for all expected resources to be available")
		case <-resourceTicker.C:
			allReady := true
			var notReadyReasons []string

			// Check tools availability
			if len(expectedTools) > 0 {
				availableTools, err := mcpClient.ListTools(resourceCtx)
				if err != nil {
					if m.debug {
						m.logger.Debug("🔍 Failed to list tools: %v\n", err)
					}
					allReady = false
					notReadyReasons = append(notReadyReasons, "tools check failed")
				} else {
					missingTools := m.findMissingTools(expectedTools, availableTools)
					if len(missingTools) > 0 {
						allReady = false
						notReadyReasons = append(notReadyReasons, fmt.Sprintf("missing tools: %v", missingTools))
					}
				}
			}

			// Check ServiceClass availability
			if len(expectedServiceClasses) > 0 {
				for _, serviceClassName := range expectedServiceClasses {
					available, err := m.checkServiceClassAvailability(mcpClient, resourceCtx, serviceClassName)
					if err != nil {
						if m.debug {
							m.logger.Debug("🔍 Failed to check ServiceClass %s: %v\n", serviceClassName, err)
						}
						allReady = false
						notReadyReasons = append(notReadyReasons, fmt.Sprintf("ServiceClass %s check failed", serviceClassName))
					} else if !available {
						allReady = false
						notReadyReasons = append(notReadyReasons, fmt.Sprintf("ServiceClass %s not available", serviceClassName))
					}
				}
			}

			// Check Service availability (if any expected)
			if len(expectedServices) > 0 {
				for _, serviceName := range expectedServices {
					available, err := m.checkServiceAvailability(mcpClient, resourceCtx, serviceName)
					if err != nil {
						if m.debug {
							m.logger.Debug("🔍 Failed to check Service %s: %v\n", serviceName, err)
						}
						allReady = false
						notReadyReasons = append(notReadyReasons, fmt.Sprintf("Service %s check failed", serviceName))
					} else if !available {
						allReady = false
						notReadyReasons = append(notReadyReasons, fmt.Sprintf("Service %s not available", serviceName))
					}
				}
			}

			// Check Workflow availability (if any expected)
			if len(expectedWorkflows) > 0 {
				availableWorkflows, err := m.checkWorkflowsAvailability(mcpClient, resourceCtx)
				if err != nil {
					if m.debug {
						m.logger.Debug("🔍 Failed to list workflows: %v\n", err)
					}
					allReady = false
					notReadyReasons = append(notReadyReasons, "workflows check failed")
				} else {
					for _, workflowName := range expectedWorkflows {
						found := false
						for _, available := range availableWorkflows {
							if available == workflowName {
								found = true
								break
							}
						}
						if !found {
							allReady = false
							notReadyReasons = append(notReadyReasons, fmt.Sprintf("Workflow %s not available", workflowName))
						}
					}
				}
			}

			// Check Capability availability (if any expected)
			if len(expectedCapabilities) > 0 {
				availableCapabilities, err := m.checkCapabilitiesAvailability(mcpClient, resourceCtx)
				if err != nil {
					if m.debug {
						m.logger.Debug("🔍 Failed to list capabilities: %v\n", err)
					}
					allReady = false
					notReadyReasons = append(notReadyReasons, "capabilities check failed")
				} else {
					for _, capabilityName := range expectedCapabilities {
						found := false
						for _, available := range availableCapabilities {
							if available == capabilityName {
								found = true
								break
							}
						}
						if !found {
							allReady = false
							notReadyReasons = append(notReadyReasons, fmt.Sprintf("Capability %s not available", capabilityName))
						}
					}
				}
			}

			if allReady {
				if m.debug {
					m.logger.Debug("✅ All expected resources are available!\n")
				}
				// Wait a little bit more to ensure everything is fully stable
				time.Sleep(2 * time.Second)
				return nil
			}

			if m.debug {
				m.logger.Debug("⏳ Still waiting for resources: %v\n", notReadyReasons)
			}
		}
	}
}

// extractExpectedTools extracts expected tool names from the configuration during instance creation
func (m *musterInstanceManager) extractExpectedTools(config *MusterPreConfiguration) []string {
	if config == nil {
		return []string{}
	}

	var expectedTools []string

	// Extract tools from MCP server configurations
	for _, mcpServer := range config.MCPServers {
		if tools, hasTools := mcpServer.Config["tools"]; hasTools {
			if toolsList, ok := tools.([]interface{}); ok {
				for _, tool := range toolsList {
					if toolMap, ok := tool.(map[string]interface{}); ok {
						if name, ok := toolMap["name"].(string); ok {
							// For MCP server tools, expect them to be available with x_<server-name>_<tool-name> prefix
							prefixedName := fmt.Sprintf("x_%s_%s", mcpServer.Name, name)
							expectedTools = append(expectedTools, prefixedName)
						}
					}
				}
			}
		}
	}

	if m.debug && len(expectedTools) > 0 {
		m.logger.Debug("🎯 Extracted expected tools from configuration: %v\n", expectedTools)
	}

	return expectedTools
}

// extractExpectedToolsFromInstance gets the expected tools stored in the instance
func (m *musterInstanceManager) extractExpectedToolsFromInstance(instance *MusterInstance) []string {
	return instance.ExpectedTools
}

// findMissingTools returns tools that are expected but not found in available tools
func (m *musterInstanceManager) findMissingTools(expectedTools, availableTools []string) []string {
	var missing []string

	for _, expected := range expectedTools {
		found := false
		for _, available := range availableTools {
			// Check for exact match
			if available == expected {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, expected)
		}
	}

	return missing
}

// isToolMatch checks if an available tool matches an expected tool name
// This handles cases where tools might have prefixes from MCP server names
func (m *musterInstanceManager) isToolMatch(availableTool, expectedTool string) bool {
	// Check exact match
	if availableTool == expectedTool {
		return true
	}

	// This method is no longer used since we now generate the correct expected tool names
	// with x_ prefix in extractExpectedTools
	return false
}

// showLogs displays the recent logs from an muster instance
func (m *musterInstanceManager) showLogs(instance *MusterInstance) {
	logDir := filepath.Join(instance.ConfigPath, "logs")

	// Show stdout logs
	stdoutPath := filepath.Join(logDir, "stdout.log")
	if content, err := os.ReadFile(stdoutPath); err == nil && len(content) > 0 {
		m.logger.Debug("📄 Instance %s stdout logs:\n%s\n", instance.ID, string(content))
	}

	// Show stderr logs
	stderrPath := filepath.Join(logDir, "stderr.log")
	if content, err := os.ReadFile(stderrPath); err == nil && len(content) > 0 {
		m.logger.Debug("🚨 Instance %s stderr logs:\n%s\n", instance.ID, string(content))
	}
}

// findAvailablePort finds an available port starting from the base port with atomic reservation
func (m *musterInstanceManager) findAvailablePort(instanceID string) (int, error) {
	m.portMu.Lock()
	defer m.portMu.Unlock()

	for i := 0; i < 100; i++ { // Try up to 100 ports
		port := m.basePort + m.portOffset + i

		// Check if already reserved by another instance
		if existingInstanceID, reserved := m.reservedPorts[port]; reserved {
			if m.debug {
				m.logger.Debug("🔒 Port %d already reserved by instance %s, skipping\n", port, existingInstanceID)
			}
			continue
		}

		// Check if port is actually available in general
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			if m.debug {
				m.logger.Debug("🔍 Port %d not available (in use): %v\n", port, err)
			}
			continue // Port not available, try next
		}

		ln.Close() // Close immediately to free the port

		// ATOMIC: Reserve the port and update offset
		m.reservedPorts[port] = instanceID
		m.portOffset = i + 1 // Next search starts from next port

		if m.debug {
			m.logger.Debug("✅ Reserved port %d for instance %s\n", port, instanceID)
		}

		return port, nil
	}

	return 0, fmt.Errorf("no available ports found starting from %d (tried 100 ports)", m.basePort)
}

// releasePort releases a reserved port back to the available pool
func (m *musterInstanceManager) releasePort(port int, instanceID string) {
	m.portMu.Lock()
	defer m.portMu.Unlock()

	// Check if the port is actually reserved by this instance
	if existingInstanceID, reserved := m.reservedPorts[port]; reserved {
		if existingInstanceID == instanceID {
			delete(m.reservedPorts, port)
			if m.debug {
				m.logger.Debug("🔓 Released port %d from instance %s\n", port, instanceID)
			}
		} else {
			if m.debug {
				m.logger.Debug("⚠️  Port %d was reserved by different instance %s, not releasing\n", port, existingInstanceID)
			}
		}
	} else {
		if m.debug {
			m.logger.Debug("ℹ️  Port %d was not reserved, nothing to release\n", port)
		}
	}
}

// startMusterProcess starts an muster serve process
func (m *musterInstanceManager) startMusterProcess(ctx context.Context, configPath string, port int) (*managedProcess, error) {
	// Get the path to the muster binary
	musterPath, err := m.getMusterBinaryPath()
	if err != nil {
		return nil, fmt.Errorf("failed to find muster binary: %w", err)
	}

	// muster serve should use the muster subdirectory as config path
	musterConfigPath := filepath.Join(configPath, "muster")

	// Create command
	args := []string{
		"serve",
		"--config-path", musterConfigPath,
		"--debug",
	}

	cmd := exec.CommandContext(ctx, musterPath, args...)

	// Configure the process attributes (platform-specific)
	configureProcAttr(cmd)

	if m.debug {
		m.logger.Debug("🚀 Starting command: %s %v\n", musterPath, args)
	}

	// Create log capture
	logCapture := newLogCapture()

	// Set up stdout/stderr capture
	cmd.Stdout = logCapture.stdoutWriter
	cmd.Stderr = logCapture.stderrWriter

	// Start the process
	if err := cmd.Start(); err != nil {
		logCapture.close()
		return nil, fmt.Errorf("failed to start muster process: %w", err)
	}

	managedProc := &managedProcess{
		cmd:        cmd,
		logCapture: logCapture,
	}

	return managedProc, nil
}

// getMusterBinaryPath returns the path to the muster binary
func (m *musterInstanceManager) getMusterBinaryPath() (string, error) {
	// First try to find in PATH
	if path, err := exec.LookPath("muster"); err == nil {
		return path, nil
	}

	// Try common locations relative to current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	// Check if we're in the muster project root
	possiblePaths := []string{
		filepath.Join(cwd, "muster"),
		filepath.Join(cwd, "bin", "muster"),
		filepath.Join(cwd, "..", "muster"),
		filepath.Join(cwd, "..", "bin", "muster"),
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// Try to build muster if we're in the source directory
	if m.isInMusterSource(cwd) {
		if m.debug {
			m.logger.Debug("🔨 Building muster binary from source\n")
		}

		buildCmd := exec.Command("go", "build", "-o", "muster", ".")
		buildCmd.Dir = cwd
		if err := buildCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to build muster: %w", err)
		}

		builtPath := filepath.Join(cwd, "muster")
		if _, err := os.Stat(builtPath); err == nil {
			return builtPath, nil
		}
	}

	return "", fmt.Errorf("muster binary not found")
}

// isInMusterSource checks if we're in the muster source directory
func (m *musterInstanceManager) isInMusterSource(dir string) bool {
	// Check for key files that indicate we're in the muster source
	markers := []string{"main.go", "go.mod", "cmd/serve.go"}

	for _, marker := range markers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			return false
		}
	}

	return true
}

// generateConfigFiles generates configuration files for the muster instance
func (m *musterInstanceManager) generateConfigFiles(configPath string, config *MusterPreConfiguration, port int) error {
	// Create muster subdirectory - this is where muster serve will look for configs
	musterConfigPath := filepath.Join(configPath, "muster")

	// Create subdirectories under muster
	dirs := []string{"mcpservers", "workflows", "capabilities", "serviceclasses", "services"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(musterConfigPath, dir), 0755); err != nil {
			return fmt.Errorf("failed to create %s directory: %w", dir, err)
		}
	}

	// Create mocks directory for mock configurations
	if err := os.MkdirAll(filepath.Join(configPath, "mocks"), 0755); err != nil {
		return fmt.Errorf("failed to create mocks directory: %w", err)
	}

	// Generate main config.yaml in muster subdirectory
	mainConfig := map[string]interface{}{
		"aggregator": map[string]interface{}{
			"host":      "localhost",
			"port":      port,
			"transport": "streamable-http",
			"enabled":   true,
		},
		"logging": map[string]interface{}{
			"level": "debug",
		},
	}

	// Apply custom main config if provided
	if config != nil && config.MainConfig != nil {
		for key, value := range config.MainConfig.Config {
			mainConfig[key] = value
		}
	}

	configFile := filepath.Join(musterConfigPath, "config.yaml")
	if err := m.writeYAMLFile(configFile, mainConfig); err != nil {
		return fmt.Errorf("failed to write main config: %w", err)
	}

	if m.debug {
		// Show the generated config
		configContent, _ := os.ReadFile(configFile)
		m.logger.Debug("📋 Generated config.yaml:\n%s\n", string(configContent))
	}

	// Generate configuration files if config is provided
	if config != nil {
		// Generate MCP server configs
		for _, mcpServer := range config.MCPServers {
			// Check if this is a mock server (has tools in config)
			if tools, hasMockTools := mcpServer.Config["tools"]; hasMockTools {
				// Get the current working directory to build the muster command path
				musterPath, err := m.getMusterBinaryPath()
				if err != nil {
					return fmt.Errorf("failed to get muster binary path: %w", err)
				}

				// Create the localCommand server definition for muster serve
				mockConfigFile := filepath.Join(configPath, "mocks", mcpServer.Name+".yaml")
				serverDef := map[string]interface{}{
					"name":      mcpServer.Name,
					"type":      "localCommand",
					"autoStart": true,
					"command":   []string{musterPath, "test", "--mock-mcp-server", "--mock-config", mockConfigFile},
				}

				if m.debug {
					m.logger.Debug("🧪 ServerDef for %s: %+v\n", mcpServer.Name, serverDef)
					m.logger.Debug("🧪 Tools config for %s: %+v\n", mcpServer.Name, mcpServer.Config)
				}

				// Save server definition to mcpservers directory (what muster serve loads)
				filename := filepath.Join(musterConfigPath, "mcpservers", mcpServer.Name+".yaml")
				if err := m.writeYAMLFile(filename, serverDef); err != nil {
					return fmt.Errorf("failed to write mock MCP server config %s: %w", mcpServer.Name, err)
				}

				// Save mock tools config to mocks directory (what mock server reads)
				if err := m.writeYAMLFile(mockConfigFile, mcpServer.Config); err != nil {
					return fmt.Errorf("failed to write mock config %s: %w", mcpServer.Name, err)
				}

				if m.debug {
					m.logger.Debug("🧪 Created mock server %s with %d tools\n", mcpServer.Name, len(tools.([]interface{})))
				}
			} else {
				// For regular servers, use the Config field directly
				filename := filepath.Join(musterConfigPath, "mcpservers", mcpServer.Name+".yaml")
				if err := m.writeYAMLFile(filename, mcpServer.Config); err != nil {
					return fmt.Errorf("failed to write MCP server config %s: %w", mcpServer.Name, err)
				}
			}
		}

		// Generate workflow configs in muster subdirectory
		for _, workflow := range config.Workflows {
			filename := filepath.Join(musterConfigPath, "workflows", workflow.Name+".yaml")
			if err := m.writeYAMLFile(filename, workflow.Config); err != nil {
				return fmt.Errorf("failed to write workflow config %s: %w", workflow.Name, err)
			}
		}

		// Generate capability configs in muster subdirectory
		for _, capability := range config.Capabilities {
			filename := filepath.Join(musterConfigPath, "capabilities", capability.Name+".yaml")
			if err := m.writeYAMLFile(filename, capability.Config); err != nil {
				return fmt.Errorf("failed to write capability config %s: %w", capability.Name, err)
			}
		}

		// Generate service class configs in muster subdirectory
		for _, serviceClass := range config.ServiceClasses {
			filename := filepath.Join(musterConfigPath, "serviceclasses", serviceClass.Name+".yaml")
			if err := m.writeYAMLFile(filename, serviceClass.Config); err != nil {
				return fmt.Errorf("failed to write service class config %s: %w", serviceClass.Name, err)
			}
		}

		// Generate service configs in muster subdirectory
		for _, service := range config.Services {
			filename := filepath.Join(musterConfigPath, "services", service.Name+".yaml")
			if err := m.writeYAMLFile(filename, service.Config); err != nil {
				return fmt.Errorf("failed to write service config %s: %w", service.Name, err)
			}
		}
	}

	return nil
}

// writeYAMLFile writes data to a YAML file
func (m *musterInstanceManager) writeYAMLFile(filename string, data interface{}) error {
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(filename, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	if m.debug {
		m.logger.Debug("📝 Generated config file: %s\n", filename)
		m.logger.Debug("📄 Content:\n%s\n", string(yamlData))
	}

	return nil
}

// sanitizeFileName sanitizes a string to be safe for use as a filename
func sanitizeFileName(name string) string {
	// Replace invalid characters with underscores
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)

	sanitized := replacer.Replace(name)

	// Limit length
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}

	return sanitized
}

// Cleanup cleans up all temporary directories created by this manager
func (m *musterInstanceManager) Cleanup() error {
	if m.tempDir != "" {
		return os.RemoveAll(m.tempDir)
	}
	return nil
}

// extractExpectedServiceClasses extracts expected ServiceClass names from the configuration during instance creation
func (m *musterInstanceManager) extractExpectedServiceClasses(config *MusterPreConfiguration) []string {
	if config == nil {
		return []string{}
	}

	var expectedServiceClasses []string

	// Extract ServiceClass names from service class configurations
	for _, serviceClass := range config.ServiceClasses {
		expectedServiceClasses = append(expectedServiceClasses, serviceClass.Name)
	}

	if m.debug && len(expectedServiceClasses) > 0 {
		m.logger.Debug("🎯 Extracted expected ServiceClasses from configuration: %v\n", expectedServiceClasses)
	}

	return expectedServiceClasses
}

// extractExpectedServiceClassesFromInstance extracts expected ServiceClass names from instance configuration
func (m *musterInstanceManager) extractExpectedServiceClassesFromInstance(instance *MusterInstance) []string {
	// Return the ServiceClasses stored during instance creation
	return instance.ExpectedServiceClasses
}

// extractExpectedServicesFromInstance extracts expected Service names from instance configuration
func (m *musterInstanceManager) extractExpectedServicesFromInstance(instance *MusterInstance) []string {
	// For now, we'll extract this from the instance configuration stored during CreateInstance
	// In a future enhancement, we could store this information in the MusterInstance struct
	return []string{} // TODO: Extract from stored configuration
}

// extractExpectedWorkflowsFromInstance extracts expected Workflow names from instance configuration
func (m *musterInstanceManager) extractExpectedWorkflowsFromInstance(instance *MusterInstance) []string {
	// For now, we'll extract this from the instance configuration stored during CreateInstance
	// In a future enhancement, we could store this information in the MusterInstance struct
	return []string{} // TODO: Extract from stored configuration
}

// extractExpectedCapabilitiesFromInstance extracts expected Capability names from instance configuration
func (m *musterInstanceManager) extractExpectedCapabilitiesFromInstance(instance *MusterInstance) []string {
	// For now, we'll extract this from the instance configuration stored during CreateInstance
	// In a future enhancement, we could store this information in the MusterInstance struct
	return []string{} // TODO: Extract from stored configuration
}

// checkServiceClassAvailability checks if a ServiceClass is available and ready
func (m *musterInstanceManager) checkServiceClassAvailability(client MCPTestClient, ctx context.Context, serviceClassName string) (bool, error) {
	// Use core_serviceclass_available to check availability
	args := map[string]interface{}{
		"name": serviceClassName,
	}

	result, err := client.CallTool(ctx, "core_serviceclass_available", args)
	if err != nil {
		return false, fmt.Errorf("failed to call core_serviceclass_available: %w", err)
	}

	if m.debug {
		m.logger.Debug("🔍 ServiceClass availability check result for %s: %+v\n", serviceClassName, result)
	}

	// Try to extract the JSON content from the MCP response
	// The response structure should have a Content field with text content
	jsonStr := ""

	// Method 1: Try reflection to access the Content field dynamically
	resultValue := reflect.ValueOf(result)
	if resultValue.Kind() == reflect.Ptr {
		resultValue = resultValue.Elem()
	}

	if resultValue.Kind() == reflect.Struct {
		contentField := resultValue.FieldByName("Content")
		if contentField.IsValid() && contentField.Kind() == reflect.Slice && contentField.Len() > 0 {
			firstContent := contentField.Index(0)
			if firstContent.Kind() == reflect.Struct {
				textField := firstContent.FieldByName("Text")
				if textField.IsValid() && textField.Kind() == reflect.String {
					jsonStr = textField.String()
				}
			}
		}
	}

	// Method 2: If reflection didn't work, try marshaling and parsing the JSON representation
	if jsonStr == "" {
		if resultBytes, err := json.Marshal(result); err == nil {
			var tempMap map[string]interface{}
			if err := json.Unmarshal(resultBytes, &tempMap); err == nil {
				if content, exists := tempMap["content"]; exists {
					if contentArray, ok := content.([]interface{}); ok && len(contentArray) > 0 {
						if contentItem, ok := contentArray[0].(map[string]interface{}); ok {
							if textContent, exists := contentItem["text"]; exists {
								if textStr, ok := textContent.(string); ok {
									jsonStr = textStr
								}
							}
						}
					}
				}
			}
		}
	}

	// Parse the extracted JSON string
	if jsonStr != "" {
		var serviceClassInfo map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &serviceClassInfo); err == nil {
			if available, exists := serviceClassInfo["available"]; exists {
				if availableBool, ok := available.(bool); ok {
					if m.debug {
						m.logger.Debug("✅ ServiceClass %s availability: %v\n", serviceClassName, availableBool)
					}
					return availableBool, nil
				}
			}
		} else {
			if m.debug {
				m.logger.Debug("🔍 Failed to parse JSON from text field: %v, content: %s\n", err, jsonStr)
			}
		}
	}

	// If we get here, the parsing failed - let's add more debugging
	if m.debug {
		m.logger.Debug("🔍 ServiceClass availability check failed to parse response: %+v\n", result)
		if resultBytes, err := json.MarshalIndent(result, "", "  "); err == nil {
			m.logger.Debug("🔍 Full response JSON:\n%s\n", string(resultBytes))
		}
	}

	return false, fmt.Errorf("unexpected response format from core_serviceclass_available")
}

// checkServiceAvailability checks if a Service is available
func (m *musterInstanceManager) checkServiceAvailability(client MCPTestClient, ctx context.Context, serviceName string) (bool, error) {
	// Use core_service_get to check if service exists
	args := map[string]interface{}{
		"name": serviceName,
	}

	result, err := client.CallTool(ctx, "core_service_get", args)
	if err != nil {
		return false, fmt.Errorf("failed to call core_service_get: %w", err)
	}

	// If the call succeeds, the service exists (result != nil means success)
	return result != nil, nil
}

// checkWorkflowsAvailability returns the list of available workflows
func (m *musterInstanceManager) checkWorkflowsAvailability(client MCPTestClient, ctx context.Context) ([]string, error) {
	// Use core_workflow_list to get available workflows
	result, err := client.CallTool(ctx, "core_workflow_list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("failed to call core_workflow_list: %w", err)
	}

	var workflows []string
	// Parse the response to extract workflow names
	if resultData, ok := result.(map[string]interface{}); ok {
		if workflowList, exists := resultData["workflows"]; exists {
			if workflowArray, ok := workflowList.([]interface{}); ok {
				for _, workflow := range workflowArray {
					if workflowMap, ok := workflow.(map[string]interface{}); ok {
						if name, exists := workflowMap["name"]; exists {
							if nameStr, ok := name.(string); ok {
								workflows = append(workflows, nameStr)
							}
						}
					}
				}
			}
		}
	}

	return workflows, nil
}

// checkCapabilitiesAvailability returns the list of available capabilities
func (m *musterInstanceManager) checkCapabilitiesAvailability(client MCPTestClient, ctx context.Context) ([]string, error) {
	// Use core_capability_list to get available capabilities
	result, err := client.CallTool(ctx, "core_capability_list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("failed to call core_capability_list: %w", err)
	}

	var capabilities []string
	// Parse the response to extract capability names
	if resultData, ok := result.(map[string]interface{}); ok {
		if capabilityList, exists := resultData["capabilities"]; exists {
			if capabilityArray, ok := capabilityList.([]interface{}); ok {
				for _, capability := range capabilityArray {
					if capabilityMap, ok := capability.(map[string]interface{}); ok {
						if name, exists := capabilityMap["name"]; exists {
							if nameStr, ok := name.(string); ok {
								capabilities = append(capabilities, nameStr)
							}
						}
					}
				}
			}
		}
	}

	return capabilities, nil
}
