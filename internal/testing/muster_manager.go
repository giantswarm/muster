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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/giantswarm/muster/internal/testing/mock"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"gopkg.in/yaml.v3"
)

const keyEnabled = "enabled"

// toStringMap converts an interface{} to map[string]interface{}.
// This handles both map[string]interface{} and map[interface{}]interface{}
// (which is common when parsing YAML).
// Returns nil, false if the conversion is not possible.
func toStringMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]interface{}); ok {
		return m, true
	}
	if m, ok := v.(map[interface{}]interface{}); ok {
		result := make(map[string]interface{})
		for key, val := range m {
			if keyStr, ok := key.(string); ok {
				result[keyStr] = val
			}
		}
		return result, true
	}
	return nil, false
}

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

// captureOutput captures output from a reader to a buffer.
//
// It uses a bufio.Reader rather than a bufio.Scanner because a Scanner aborts
// on any line longer than its token limit (64 KiB by default). When that
// happens the goroutine stops draining the io.Pipe, the subprocess blocks on
// its next write, and the whole instance deadlocks. A debug-level log line
// carrying a large workflow result easily exceeds that limit, so reading
// length-unbounded lines keeps the subprocess from ever stalling on output.
func (lc *logCapture) captureOutput(reader io.Reader, buffer *bytes.Buffer) {
	defer lc.wg.Done()

	br := bufio.NewReader(reader)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			lc.mu.Lock()
			buffer.WriteString(line)
			lc.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// close closes the capture pipes and waits for completion
func (lc *logCapture) close() {
	_ = lc.stdoutWriter.Close()
	_ = lc.stderrWriter.Close()
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

// managedProcess represents a managed muster process with its command and log capture.
//
// A single goroutine owns cmd.Wait(): it stores the result in waitErr and closes
// exited. Other goroutines (WaitForReady, gracefulShutdown) observe termination
// by selecting on exited instead of calling cmd.Wait() themselves, which would
// panic with "Wait was already called".
type managedProcess struct {
	cmd        *exec.Cmd
	logCapture *logCapture
	exited     chan struct{} // closed once the process has exited
	waitErr    error         // result of cmd.Wait(); read only after exited is closed
}

// musterInstanceManager implements the MusterInstanceManager interface
type musterInstanceManager struct {
	debug          bool
	basePort       int
	portOffset     int
	tempDir        string
	processes      map[string]*managedProcess // Track processes by instance ID
	mu             sync.RWMutex
	logger         TestLogger
	keepTempConfig bool

	// Port reservation system for thread-safe parallel execution
	portMu        sync.Mutex     // Protects port allocation
	reservedPorts map[int]string // port -> instanceID mapping

	// reservedListeners holds the probe listener for each reserved port open
	// until muster serve is about to bind it. Keeping the socket bound prevents
	// the OS from handing the freed port to an ephemeral listener (e.g. a mock
	// server's net.Listen(":0")) while the instance finishes its setup, which
	// would otherwise make muster serve fail to bind. Protected by portMu.
	reservedListeners map[int]net.Listener

	// ephemeralLow/ephemeralHigh describe the OS ephemeral port range. Ports in
	// this range are preferred-against when allocating instance ports: there is
	// an unavoidable sub-millisecond window between closing the reserved probe
	// listener and muster serve binding the port (see startMusterProcess), and
	// during it a concurrent mock server's net.Listen(":0") can be handed that
	// exact port by the OS. Allocating muster ports outside the ephemeral range
	// makes that collision impossible regardless of the configured base port.
	ephemeralLow  int
	ephemeralHigh int

	// Mock HTTP server tracking for URL-based mock MCP servers
	mockHTTPServers map[string]map[string]*mock.HTTPServer // instanceID -> serverName -> server

	// Mock OAuth server tracking
	mockOAuthServers map[string]map[string]*mock.OAuthServer // instanceID -> serverName -> server

	// Protected MCP server tracking (OAuth-protected mock MCP servers)
	protectedMCPServers map[string]map[string]*mock.ProtectedMCPServer // instanceID -> serverName -> server
}

// NewMusterInstanceManagerWithLogger creates a new muster instance manager with custom logger
func NewMusterInstanceManagerWithLogger(debug bool, basePort int, logger TestLogger) (MusterInstanceManager, error) {
	return NewMusterInstanceManagerWithConfig(debug, basePort, logger, false)
}

// NewMusterInstanceManagerWithConfig creates a new muster instance manager with custom logger and config options
func NewMusterInstanceManagerWithConfig(debug bool, basePort int, logger TestLogger, keepTempConfig bool) (MusterInstanceManager, error) {
	// Create temporary directory for test configurations
	tempDir, err := os.MkdirTemp("", "muster-test-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	ephemeralLow, ephemeralHigh := detectEphemeralPortRange()

	return &musterInstanceManager{
		debug:               debug,
		basePort:            basePort,
		tempDir:             tempDir,
		processes:           make(map[string]*managedProcess),
		logger:              logger,
		keepTempConfig:      keepTempConfig,
		reservedPorts:       make(map[int]string),
		reservedListeners:   make(map[int]net.Listener),
		ephemeralLow:        ephemeralLow,
		ephemeralHigh:       ephemeralHigh,
		mockHTTPServers:     make(map[string]map[string]*mock.HTTPServer),
		mockOAuthServers:    make(map[string]map[string]*mock.OAuthServer),
		protectedMCPServers: make(map[string]map[string]*mock.ProtectedMCPServer),
	}, nil
}

// CreateInstance creates a new muster serve instance with the given configuration.
// The logger parameter allows scenario-specific logging with prefixes for parallel execution.
func (m *musterInstanceManager) CreateInstance(ctx context.Context, scenarioName string, config *MusterPreConfiguration, logger TestLogger) (*MusterInstance, error) {
	// Use provided logger or fall back to manager's logger
	if logger == nil {
		logger = m.logger
	}

	// Generate unique instance ID
	instanceID := fmt.Sprintf("test-%s-%d", sanitizeFileName(scenarioName), time.Now().UnixNano())

	// Find available port (with atomic reservation)
	port, err := m.findAvailablePort(instanceID, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to find available port: %w", err)
	}

	// Create instance configuration directory
	configPath := filepath.Join(m.tempDir, instanceID)
	if err := os.MkdirAll(configPath, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	if m.debug {
		logger.Debug("🏗️  Creating muster instance %s with config at %s\n", instanceID, configPath)
	}

	// Start mock OAuth servers FIRST (before HTTP servers, as they may depend on OAuth)
	mockOAuthServerInfo, err := m.startMockOAuthServers(ctx, instanceID, config, logger)
	if err != nil {
		m.releasePort(port, instanceID, logger)
		_ = os.RemoveAll(configPath)
		return nil, fmt.Errorf("failed to start mock OAuth servers: %w", err)
	}

	// Start mock HTTP servers for URL-based mock MCP servers BEFORE generating config files
	// Pass OAuth server info so protected MCP servers can reference them
	mockHTTPServerInfo, err := m.startMockHTTPServersWithOAuth(ctx, instanceID, configPath, port, config, mockOAuthServerInfo, logger)
	if err != nil {
		m.stopMockOAuthServers(ctx, instanceID, logger)
		m.releasePort(port, instanceID, logger)
		_ = os.RemoveAll(configPath)
		return nil, fmt.Errorf("failed to start mock HTTP servers: %w", err)
	}

	// Generate configuration files (passing mock HTTP server endpoints)
	if err := m.generateConfigFilesWithMocks(configPath, config, port, mockHTTPServerInfo, instanceID, logger); err != nil {
		// Clean up mock HTTP servers on failure
		m.stopMockHTTPServers(ctx, instanceID, logger)
		m.releasePort(port, instanceID, logger)
		_ = os.RemoveAll(configPath)
		return nil, fmt.Errorf("failed to generate config files: %w", err)
	}

	// Start muster serve process with log capture
	managedProc, err := m.startMusterProcess(ctx, configPath, port, logger)
	if err != nil {
		// Clean up on failure: stop mock servers, release port and remove config directory
		m.stopMockHTTPServers(ctx, instanceID, logger)
		m.releasePort(port, instanceID, logger)
		_ = os.RemoveAll(configPath)
		return nil, fmt.Errorf("failed to start muster process: %w", err)
	}

	// Store the managed process
	m.mu.Lock()
	m.processes[instanceID] = managedProc
	m.mu.Unlock()

	// Extract expected resources from configuration
	expectedTools := m.extractExpectedToolsWithHTTPMocks(config, mockHTTPServerInfo)
	expectedMCPServers := m.extractExpectedMCPServers(config)

	// Extract muster OAuth access token if any mock OAuth server is used as muster's OAuth server
	var musterOAuthToken string
	for _, oauthInfo := range mockOAuthServerInfo {
		if oauthInfo.AccessToken != "" {
			musterOAuthToken = oauthInfo.AccessToken
			break
		}
	}

	instance := &MusterInstance{
		ID:                     instanceID,
		ConfigPath:             configPath,
		Port:                   port,
		Endpoint:               fmt.Sprintf("http://localhost:%d/mcp", port),
		Process:                managedProc.cmd.Process,
		StartTime:              time.Now(),
		Logs:                   nil, // Will be populated when destroying
		ExpectedTools:          expectedTools,
		ExpectedMCPServers:     expectedMCPServers,
		MockHTTPServers:        mockHTTPServerInfo,
		MockOAuthServers:       mockOAuthServerInfo,
		MusterOAuthAccessToken: musterOAuthToken,
	}

	if m.debug {
		logger.Debug("🚀 Started muster instance %s on port %d (PID: %d)\n", instanceID, port, managedProc.cmd.Process.Pid)
	}

	return instance, nil
}

// DestroyInstance stops and cleans up an muster serve instance.
// The logger parameter allows scenario-specific logging with prefixes for parallel execution.
func (m *musterInstanceManager) DestroyInstance(ctx context.Context, instance *MusterInstance, logger TestLogger) error {
	// Use provided logger or fall back to manager's logger
	if logger == nil {
		logger = m.logger
	}

	if m.debug {
		logger.Debug("🛑 Destroying muster instance %s (PID: %d)\n", instance.ID, instance.Process.Pid)
	}

	// Get the managed process
	m.mu.RLock()
	managedProc, exists := m.processes[instance.ID]
	m.mu.RUnlock()

	if exists && managedProc != nil {
		// Attempt graceful shutdown first
		if err := m.gracefulShutdown(managedProc, instance.ID, logger); err != nil {
			if m.debug {
				logger.Debug("⚠️  Graceful shutdown failed for %s: %v, forcing termination\n", instance.ID, err)
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

	// Stop protected MCP servers for this instance
	m.stopProtectedMCPServers(ctx, instance.ID, logger)

	// Stop mock HTTP servers for this instance
	m.stopMockHTTPServers(ctx, instance.ID, logger)

	// Stop mock OAuth servers for this instance
	m.stopMockOAuthServers(ctx, instance.ID, logger)

	// Release the reserved port
	m.releasePort(instance.Port, instance.ID, logger)

	// Clean up configuration directory unless keepTempConfig is true
	if m.keepTempConfig {
		if m.debug {
			logger.Debug("🔍 Keeping temporary config directory for debugging: %s\n", instance.ConfigPath)
		}
	} else {
		if err := os.RemoveAll(instance.ConfigPath); err != nil {
			if m.debug {
				logger.Debug("⚠️  Failed to remove config directory %s: %v\n", instance.ConfigPath, err)
			}
			return fmt.Errorf("failed to remove config directory: %w", err)
		}
	}

	if m.debug {
		logger.Debug("✅ Destroyed muster instance %s\n", instance.ID)
	}

	return nil
}

// gracefulShutdown attempts to gracefully shutdown an muster process and all its children
func (m *musterInstanceManager) gracefulShutdown(managedProc *managedProcess, instanceID string, logger TestLogger) error {
	if managedProc.cmd == nil || managedProc.cmd.Process == nil {
		return fmt.Errorf("no process to shutdown")
	}

	process := managedProc.cmd.Process

	if m.debug {
		logger.Debug("🛑 Shutting down process group for %s (PID: %d)\n", instanceID, process.Pid)
	}

	// First, send SIGTERM to the entire process group to terminate all children
	if err := m.killProcessGroup(process.Pid, syscall.SIGTERM); err != nil {
		if m.debug {
			logger.Debug("⚠️  Failed to send SIGTERM to process group %d: %v\n", process.Pid, err)
		}
	}

	// Wait for graceful shutdown with timeout. cmd.Wait() is owned by the
	// goroutine started in startMusterProcess; observe termination via the
	// exited channel instead of calling Wait() again.
	shutdownTimeout := 10 * time.Second

	select {
	case <-managedProc.exited:
		if m.debug {
			if err := managedProc.waitErr; err != nil {
				logger.Debug("✅ Process %s exited with: %v\n", instanceID, err)
			} else {
				logger.Debug("✅ Process %s exited gracefully\n", instanceID)
			}
		}
		// Ensure any remaining child processes are killed
		_ = m.killProcessGroup(process.Pid, syscall.SIGKILL)
		return nil
	case <-time.After(shutdownTimeout):
		if m.debug {
			logger.Debug("⏰ Graceful shutdown timeout for %s, forcing kill of entire process group\n", instanceID)
		}
		// Force kill the entire process group
		return m.killProcessGroup(process.Pid, syscall.SIGKILL)
	}
}

// WaitForReady waits for an instance to be ready to accept connections and has all expected resources available.
// The logger parameter allows scenario-specific logging with prefixes for parallel execution.
func (m *musterInstanceManager) WaitForReady(ctx context.Context, instance *MusterInstance, logger TestLogger) error {
	// Use provided logger or fall back to manager's logger
	if logger == nil {
		logger = m.logger
	}

	if m.debug {
		logger.Debug("⏳ Waiting for muster instance %s to be ready at %s\n", instance.ID, instance.Endpoint)
	}

	timeout := 60 * time.Second // Increased timeout for more complex setups
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Look up the managed process so we can detect an early exit (e.g. a failed
	// port bind) instead of polling a dead port until the deadline. A nil
	// channel blocks forever, so the select degrades gracefully if absent.
	m.mu.RLock()
	managedProc := m.processes[instance.ID]
	m.mu.RUnlock()
	var procExited <-chan struct{}
	if managedProc != nil {
		procExited = managedProc.exited
	}

	// First wait for port to be available
	portReady := false
	for !portReady {
		select {
		case <-readyCtx.Done():
			if m.debug {
				m.showLogs(instance, logger)
			}
			return fmt.Errorf("timeout waiting for muster instance port to be ready")
		case <-procExited:
			// The process died before its port came up — surface this
			// immediately (with captured output) rather than waiting out the
			// full deadline, which also frees the worker slot and avoids
			// starving other parallel scenarios.
			return m.processExitedError(instance, managedProc, logger)
		case <-ticker.C:
			// Check if port is accepting connections
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", instance.Port), 1*time.Second)
			if err == nil {
				_ = conn.Close()
				portReady = true
				if m.debug {
					logger.Debug("✅ Port %d is ready\n", instance.Port)
				}
			} else if m.debug {
				logger.Debug("🔍 Port %d not ready yet: %v\n", instance.Port, err)
			}
		}
	}

	// Now wait for services to be fully initialized
	if m.debug {
		logger.Debug("⏳ Waiting for services to be fully initialized and all resources to be available...\n")
	}

	// Create MCP client to check availability
	mcpClient := NewMCPTestClient(m.debug)
	defer func() { _ = mcpClient.Close() }()

	// Connect to the MCP aggregator
	// Use authenticated connection if muster's OAuth server is enabled
	connectCtx, connectCancel := context.WithTimeout(readyCtx, 5*time.Second)
	defer connectCancel()

	// Retry connection until successful or timeout
	var connected bool
	for !connected {
		select {
		case <-connectCtx.Done():
			if m.debug {
				logger.Debug("⚠️  Failed to connect to MCP aggregator, proceeding anyway\n")
			}
			// If we can't connect to MCP, fall back to the old behavior
			time.Sleep(3 * time.Second)
			return nil
		case <-procExited:
			// The process crashed after binding its port but before the
			// aggregator was reachable — fail fast instead of retrying until
			// the connect deadline.
			return m.processExitedError(instance, managedProc, logger)
		case <-time.After(100 * time.Millisecond):
			var err error
			if instance.MusterOAuthAccessToken != "" {
				err = mcpClient.ConnectWithAuth(connectCtx, instance.Endpoint, instance.MusterOAuthAccessToken)
			} else {
				err = mcpClient.Connect(connectCtx, instance.Endpoint)
			}
			if err == nil {
				connected = true
				if m.debug {
					logger.Debug("✅ Connected to MCP aggregator\n")
				}
			} else if m.debug {
				logger.Debug("🔍 Waiting for MCP connection: %v\n", err)
			}
		}
	}

	// Extract expected resources from the pre-configuration
	expectedTools := m.extractExpectedToolsFromInstance(instance)
	expectedWorkflows := m.extractExpectedWorkflowsFromInstance(instance)
	expectedMCPServers := m.extractExpectedMCPServersFromInstance(instance)

	if len(expectedTools) == 0 && len(expectedWorkflows) == 0 && len(expectedMCPServers) == 0 {
		if m.debug {
			logger.Debug("ℹ️  No expected resources specified, waiting for basic service readiness\n")
		}
		return nil
	}

	if m.debug {
		if len(expectedTools) > 0 {
			logger.Debug("🎯 Waiting for %d expected tools: %v\n", len(expectedTools), expectedTools)
		}
		if len(expectedWorkflows) > 0 {
			logger.Debug("🎯 Waiting for %d expected Workflows: %v\n", len(expectedWorkflows), expectedWorkflows)
		}
		if len(expectedMCPServers) > 0 {
			logger.Debug("🎯 Waiting for %d expected MCP servers: %v\n", len(expectedMCPServers), expectedMCPServers)
		}
	}

	// Wait for all expected resources to be available
	// Use a longer timeout to handle high parallelism and complex OAuth setups
	resourceTimeout := 15 * time.Second
	resourceCtx, resourceCancel := context.WithTimeout(readyCtx, resourceTimeout)
	defer resourceCancel()

	resourceTicker := time.NewTicker(100 * time.Millisecond)
	defer resourceTicker.Stop()

	for {
		select {
		case <-resourceCtx.Done():
			if m.debug {
				logger.Debug("⚠️  Resource availability check timed out, checking what's available...\n")
				// Show what's available for debugging
				if len(expectedTools) > 0 {
					if availableTools, err := m.listToolsViaMeta(mcpClient, context.Background()); err == nil {
						logger.Debug("🛠️  Available tools: %v\n", availableTools)
						logger.Debug("🎯 Expected tools: %v\n", expectedTools)
					}
				}
				if len(expectedMCPServers) > 0 {
					if serverStates, err := m.checkMCPServersAvailability(mcpClient, context.Background()); err == nil {
						logger.Debug("🔌 Registered MCP server states: %v\n", serverStates)
						logger.Debug("🎯 Expected MCP servers: %v\n", expectedMCPServers)
					}
				}
			}
			return fmt.Errorf("timeout waiting for all expected resources to be available")
		case <-procExited:
			// The process died while we were waiting for its resources to
			// register — surface the crash immediately rather than polling a
			// dead aggregator until the resource deadline.
			return m.processExitedError(instance, managedProc, logger)
		case <-resourceTicker.C:
			allReady := true
			var notReadyReasons []string

			// Check tools availability via the list_tools meta-tool.
			// MCP tools/list only returns meta-tools; downstream tools are
			// discovered through the list_tools meta-tool.
			if len(expectedTools) > 0 {
				availableTools, err := m.listToolsViaMeta(mcpClient, resourceCtx)
				if err != nil {
					if m.debug {
						logger.Debug("🔍 Failed to list tools: %v\n", err)
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

			// Check Workflow availability (if any expected)
			if len(expectedWorkflows) > 0 {
				availableWorkflows, err := m.checkWorkflowsAvailability(mcpClient, resourceCtx)
				if err != nil {
					if m.debug {
						logger.Debug("🔍 Failed to list workflows: %v\n", err)
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

			// Check MCP server availability (if any expected)
			// This is critical for OAuth-protected servers which must be registered
			// before tests can call core_auth_login
			if len(expectedMCPServers) > 0 {
				serverStates, err := m.checkMCPServersAvailability(mcpClient, resourceCtx)
				if err != nil {
					if m.debug {
						logger.Debug("🔍 Failed to list MCP servers: %v\n", err)
					}
					allReady = false
					notReadyReasons = append(notReadyReasons, "MCP servers check failed")
				} else {
					missingServers := m.findMissingMCPServers(expectedMCPServers, serverStates)
					if len(missingServers) > 0 {
						allReady = false
						notReadyReasons = append(notReadyReasons, fmt.Sprintf("missing MCP servers: %v", missingServers))
					}
				}
			}

			if allReady {
				if m.debug {
					logger.Debug("✅ All expected resources are available!\n")
				}

				return nil
			}

			if m.debug {
				logger.Debug("⏳ Still waiting for resources: %v\n", notReadyReasons)
			}
		}
	}
}

// extractExpectedToolsFromInstance gets the expected tools stored in the instance
func (m *musterInstanceManager) extractExpectedToolsFromInstance(instance *MusterInstance) []string {
	return instance.ExpectedTools
}

// listToolsViaMeta queries the list_tools meta-tool to discover all available
// tools (meta-tools + downstream server tools). MCP tools/list only returns
// meta-tools, so this is the correct way to check downstream tool availability.
func (m *musterInstanceManager) listToolsViaMeta(client MCPTestClient, ctx context.Context) ([]string, error) {
	result, err := client.CallToolDirect(ctx, "list_tools", nil)
	if err != nil {
		return nil, fmt.Errorf("list_tools meta-tool call failed: %w", err)
	}

	return extractToolNamesFromResult(result)
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

// showLogs displays the recent logs from an muster instance
func (m *musterInstanceManager) showLogs(instance *MusterInstance, logger TestLogger) {
	logDir := filepath.Join(instance.ConfigPath, "logs")

	// Show stdout logs
	stdoutPath := filepath.Join(logDir, "stdout.log")
	if content, err := os.ReadFile(stdoutPath); err == nil && len(content) > 0 { //nolint:gosec
		logger.Debug("📄 Instance %s stdout logs:\n%s\n", instance.ID, string(content))
	}

	// Show stderr logs
	stderrPath := filepath.Join(logDir, "stderr.log")
	if content, err := os.ReadFile(stderrPath); err == nil && len(content) > 0 { //nolint:gosec
		logger.Debug("🚨 Instance %s stderr logs:\n%s\n", instance.ID, string(content))
	}
}

// capturedLogTail returns a short, human-readable tail of the process's captured
// stderr (falling back to stdout) for embedding in error messages. Returns an
// empty string when no output was captured. The tail is bounded so error
// messages stay readable.
func capturedLogTail(mp *managedProcess) string {
	if mp == nil || mp.logCapture == nil {
		return ""
	}
	logs := mp.logCapture.getLogs()
	out := logs.Stderr
	if strings.TrimSpace(out) == "" {
		out = logs.Stdout
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	const maxTail = 1000
	if len(out) > maxTail {
		out = "..." + out[len(out)-maxTail:]
	}
	return "\n--- captured muster output ---\n" + out
}

// processExitedError builds the standard error returned from WaitForReady when
// the muster serve process terminates before the instance becomes ready. It
// surfaces the wait result and a bounded tail of the captured output, and shows
// the full logs in debug mode. Reading managedProc.waitErr here is safe because
// callers reach this path only after observing the closed exited channel.
func (m *musterInstanceManager) processExitedError(instance *MusterInstance, managedProc *managedProcess, logger TestLogger) error {
	if m.debug {
		m.showLogs(instance, logger)
	}
	return fmt.Errorf("muster instance process exited before becoming ready: %w%s",
		managedProc.waitErr, capturedLogTail(managedProc))
}

// findAvailablePort finds an available port starting from the base port with atomic reservation
func (m *musterInstanceManager) findAvailablePort(instanceID string, logger TestLogger) (int, error) {
	m.portMu.Lock()
	defer m.portMu.Unlock()

	// First pass: prefer ports outside the OS ephemeral range. Such ports can
	// never be handed to a mock server's net.Listen(":0"), so muster serve is
	// guaranteed to bind the reserved port even though the probe listener is
	// briefly closed before exec (see reservedListeners / startMusterProcess).
	if port, ok := m.reservePortLocked(instanceID, logger, true); ok {
		return port, nil
	}

	// Fallback: the whole configured window lies inside the ephemeral range, so
	// no safe port is available. Allocate inside it anyway to preserve behavior,
	// but warn: ephemeral collisions can cause rare setup flakes. Choosing a
	// base port below the ephemeral range avoids this entirely.
	logger.Info("⚠️  base port window [%d, %d] falls inside the OS ephemeral range [%d, %d]; "+
		"instance ports may rarely be stolen by mock servers. Use a base port below %d to avoid this.\n",
		m.basePort, m.basePort+99, m.ephemeralLow, m.ephemeralHigh, m.ephemeralLow)
	if port, ok := m.reservePortLocked(instanceID, logger, false); ok {
		return port, nil
	}

	return 0, fmt.Errorf("no available ports found starting from %d (tried 100 ports)", m.basePort)
}

// reservePortLocked scans up to 100 ports from the current offset and reserves
// the first available one, returning it. When skipEphemeral is true, ports that
// fall inside the OS ephemeral range are skipped. Caller must hold portMu.
func (m *musterInstanceManager) reservePortLocked(instanceID string, logger TestLogger, skipEphemeral bool) (int, bool) {
	for i := 0; i < 100; i++ { // Try up to 100 ports
		port := m.basePort + m.portOffset + i

		// Check if already reserved by another instance
		if existingInstanceID, reserved := m.reservedPorts[port]; reserved {
			if m.debug {
				logger.Debug("🔒 Port %d already reserved by instance %s, skipping\n", port, existingInstanceID)
			}
			continue
		}

		// Skip ephemeral-range ports on the preferred pass.
		if skipEphemeral && m.isEphemeralPort(port) {
			if m.debug {
				logger.Debug("🚫 Port %d is in the OS ephemeral range [%d, %d], skipping\n", port, m.ephemeralLow, m.ephemeralHigh)
			}
			continue
		}

		// Check if port is actually available in general
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			if m.debug {
				logger.Debug("🔍 Port %d not available (in use): %v\n", port, err)
			}
			continue // Port not available, try next
		}

		// Keep the listener open (do NOT close here). Closing it now would free
		// the port at the OS level, letting an ephemeral net.Listen(":0") from a
		// mock server steal it before muster serve binds. startMusterProcess
		// closes it immediately before exec to minimize the bind race window.
		m.reservedPorts[port] = instanceID
		m.reservedListeners[port] = ln
		m.portOffset = i + 1 // Next search starts from next port

		if m.debug {
			logger.Debug("✅ Reserved port %d for instance %s\n", port, instanceID)
		}

		return port, true
	}

	return 0, false
}

// isEphemeralPort reports whether port lies within the OS ephemeral port range.
func (m *musterInstanceManager) isEphemeralPort(port int) bool {
	return port >= m.ephemeralLow && port <= m.ephemeralHigh
}

// detectEphemeralPortRange returns the OS ephemeral (local) port range. On Linux
// it reads /proc/sys/net/ipv4/ip_local_port_range; on other platforms, or if the
// value cannot be read, it falls back to the common 32768-60999 range. The
// range is used to keep test instance ports away from ports the OS may hand out
// to net.Listen(":0") callers (see musterInstanceManager.ephemeralLow).
func detectEphemeralPortRange() (int, int) {
	const fallbackLow, fallbackHigh = 32768, 60999

	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return fallbackLow, fallbackHigh
	}

	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return fallbackLow, fallbackHigh
	}

	low, errLow := strconv.Atoi(fields[0])
	high, errHigh := strconv.Atoi(fields[1])
	if errLow != nil || errHigh != nil || low <= 0 || high < low {
		return fallbackLow, fallbackHigh
	}

	return low, high
}

// closeReservedListener closes and forgets the probe listener held open for the
// given port. It is called immediately before muster serve binds the port so the
// child process can take it over. Safe to call multiple times.
func (m *musterInstanceManager) closeReservedListener(port int) {
	m.portMu.Lock()
	defer m.portMu.Unlock()
	m.releaseReservedListenerLocked(port)
}

// releaseReservedListenerLocked closes and removes the held probe listener for
// the given port. Caller must hold portMu.
func (m *musterInstanceManager) releaseReservedListenerLocked(port int) {
	if ln, ok := m.reservedListeners[port]; ok {
		_ = ln.Close()
		delete(m.reservedListeners, port)
	}
}

// releasePort releases a reserved port back to the available pool
func (m *musterInstanceManager) releasePort(port int, instanceID string, logger TestLogger) {
	m.portMu.Lock()
	defer m.portMu.Unlock()

	// Close any probe listener still held for this port (e.g. when setup failed
	// before muster serve was started). Idempotent if already closed.
	m.releaseReservedListenerLocked(port)

	// Check if the port is actually reserved by this instance
	if existingInstanceID, reserved := m.reservedPorts[port]; reserved {
		if existingInstanceID == instanceID {
			delete(m.reservedPorts, port)
			if m.debug {
				logger.Debug("🔓 Released port %d from instance %s\n", port, instanceID)
			}
		} else {
			if m.debug {
				logger.Debug("⚠️  Port %d was reserved by different instance %s, not releasing\n", port, existingInstanceID)
			}
		}
	} else {
		if m.debug {
			logger.Debug("ℹ️  Port %d was not reserved, nothing to release\n", port)
		}
	}
}

// startMusterProcess starts an muster serve process.
//
// port is the reserved port muster serve will bind. The probe listener held open
// for it (see findAvailablePort) is closed immediately before exec so the child
// can take the port over with a near-zero race window.
func (m *musterInstanceManager) startMusterProcess(ctx context.Context, configPath string, port int, logger TestLogger) (*managedProcess, error) {
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

	// If the test harness collected mock OAuth CA certs into a combined bundle
	// (see configureOAuthForInstance / collectAndWriteCACertificates), pass it
	// via --extra-ca-file so muster's process-wide http.DefaultTransport trusts
	// the self-signed test certs for OAuth proxy, token exchange, and Dex
	// provider calls.
	combinedCAFile := filepath.Join(musterConfigPath, "mock-oauth-ca.pem")
	if _, err := os.Stat(combinedCAFile); err == nil {
		args = append(args, "--extra-ca-file", combinedCAFile)
	}

	cmd := exec.CommandContext(ctx, musterPath, args...) //nolint:gosec

	// Configure the process attributes (platform-specific)
	configureProcAttr(cmd)

	if m.debug {
		logger.Debug("🚀 Starting command: %s %v\n", musterPath, args)
	}

	// Create log capture
	logCapture := newLogCapture()

	// Set up stdout/stderr capture
	cmd.Stdout = logCapture.stdoutWriter
	cmd.Stderr = logCapture.stderrWriter

	// Release the OS-level port reservation just before exec: the held listener
	// kept ephemeral listeners from stealing the port during setup, and now
	// muster serve binds it immediately, leaving only a process-startup-sized
	// race window.
	m.closeReservedListener(port)

	// Start the process
	if err := cmd.Start(); err != nil {
		logCapture.close()
		return nil, fmt.Errorf("failed to start muster process: %w", err)
	}

	managedProc := &managedProcess{
		cmd:        cmd,
		logCapture: logCapture,
		exited:     make(chan struct{}),
	}

	// A single goroutine owns cmd.Wait() so termination can be observed via the
	// exited channel from multiple places without calling Wait() twice.
	go func() {
		managedProc.waitErr = cmd.Wait()
		close(managedProc.exited)
	}()

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

// writeYAMLFile writes data to a YAML file
func (m *musterInstanceManager) writeYAMLFile(filename string, data interface{}, logger TestLogger) error {
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(filename, yamlData, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("failed to write file: %w", err)
	}

	if m.debug {
		logger.Debug("📝 Generated config file: %s\n", filename)
		logger.Debug("📄 Content:\n%s\n", string(yamlData))
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

// GetMockHTTPServer returns a mock HTTP server for the given instance and server name.
func (m *musterInstanceManager) GetMockHTTPServer(instanceID, serverName string) *mock.HTTPServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if servers, ok := m.mockHTTPServers[instanceID]; ok {
		return servers[serverName]
	}
	return nil
}

// GetProtectedMCPServer returns a protected MCP server for the given instance and server name.
func (m *musterInstanceManager) GetProtectedMCPServer(instanceID, serverName string) *mock.ProtectedMCPServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if servers, ok := m.protectedMCPServers[instanceID]; ok {
		return servers[serverName]
	}
	return nil
}

// stopMockHTTPServers stops all mock HTTP servers for a given instance
func (m *musterInstanceManager) stopMockHTTPServers(ctx context.Context, instanceID string, logger TestLogger) {
	// Use provided logger or fall back to manager's logger
	if logger == nil {
		logger = m.logger
	}

	m.mu.Lock()
	servers, exists := m.mockHTTPServers[instanceID]
	if exists {
		delete(m.mockHTTPServers, instanceID)
	}
	m.mu.Unlock()

	if !exists || len(servers) == 0 {
		return
	}

	for name, server := range servers {
		if m.debug {
			logger.Debug("🛑 Stopping mock HTTP server %s\n", name)
		}

		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := server.Stop(stopCtx); err != nil {
			if m.debug {
				logger.Debug("⚠️  Failed to stop mock HTTP server %s: %v\n", name, err)
			}
		}
		cancel()
	}
}

// configureOAuthForInstance configures OAuth proxy and server settings for a test instance.
// This is extracted from generateConfigFilesWithMocks for readability.
// Uses the consolidated OAuth config structure: oauth.mcpClient + oauth.server
func (m *musterInstanceManager) configureOAuthForInstance(
	aggregatorConfig map[string]interface{},
	config *MusterPreConfiguration,
	port int,
	instanceID string,
	musterConfigPath string,
	logger TestLogger,
) {
	// Build OAuth MCP client/proxy config - this allows muster to handle OAuth flows for protected MCP servers
	oauthMCPClientConfig := map[string]interface{}{
		keyEnabled:     true,
		"publicUrl":    fmt.Sprintf("http://localhost:%d", port),
		"callbackPath": "/oauth/proxy/callback",
	}
	if m.debug {
		logger.Debug("🔐 Enabled OAuth MCP client/proxy for test instance (publicUrl: http://localhost:%d)\n", port)
	}

	// Collect CA certificates from ALL TLS-enabled OAuth servers.
	// The combined bundle is written to musterConfigPath and passed to
	// `muster serve` via --extra-ca-file (wired in startMusterProcess),
	// which augments http.DefaultTransport process-wide so every outbound
	// HTTPS call (OAuth proxy, token exchange, Dex provider) trusts these
	// CAs. This is needed for token exchange to work, where muster needs
	// to call remote OAuth servers (e.g., cluster-b-idp) that use
	// self-signed certs.
	combinedCAFile := m.collectAndWriteCACertificates(instanceID, musterConfigPath, config, logger)
	if combinedCAFile != "" && m.debug {
		logger.Debug("🔒 Combined CA certificate written to %s\n", combinedCAFile)
	}

	// Build the consolidated OAuth config with mcpClient and server sub-sections
	oauthConfig := map[string]interface{}{
		"mcpClient": oauthMCPClientConfig,
	}

	// Check if any mock OAuth server should be used as muster's OAuth server
	// This enables testing of SSO token forwarding with muster's OAuth server protection
	for _, oauthCfg := range config.MockOAuthServers {
		if oauthCfg.UseAsMusterOAuthServer {
			oauthServerConfig := m.buildMusterOAuthServerConfig(oauthCfg, port, instanceID, oauthMCPClientConfig, logger)
			if oauthServerConfig != nil {
				oauthConfig["server"] = oauthServerConfig
			}
			break // Only one mock server can be used as muster's OAuth server
		}
	}

	// Layer the self-issued token exchange (JWT mode, trusted issuers) onto
	// muster's OAuth server config when a scenario requests it.
	if config.MusterBroker != nil {
		serverConfig, ok := oauthConfig["server"].(map[string]interface{})
		if !ok {
			logger.Debug("⚠️  muster_broker set but no mock OAuth server uses use_as_muster_oauth_server; broker not configured\n")
		} else if err := m.applyBrokerConfig(serverConfig, config, port, instanceID, musterConfigPath, logger); err != nil {
			logger.Debug("⚠️  Failed to configure muster broker: %v\n", err)
		}
	}

	aggregatorConfig["oauth"] = oauthConfig
}

// collectAndWriteCACertificates collects CA certificates from all TLS-enabled OAuth servers
// and writes them to a combined CA file that muster can trust.
// This is essential for token exchange scenarios where muster needs to call remote OAuth
// servers (like cluster-b-idp) that use self-signed certificates.
func (m *musterInstanceManager) collectAndWriteCACertificates(
	instanceID string,
	musterConfigPath string,
	config *MusterPreConfiguration,
	logger TestLogger,
) string {
	m.mu.RLock()
	oauthServers := m.mockOAuthServers[instanceID]
	m.mu.RUnlock()

	if len(oauthServers) == 0 {
		return ""
	}

	// Collect all CA certificates
	var combinedCAPEM []byte
	for serverName, server := range oauthServers {
		if server.IsTLS() {
			caPEM := server.GetCACertPEM()
			if len(caPEM) > 0 {
				combinedCAPEM = append(combinedCAPEM, caPEM...)
				if m.debug {
					logger.Debug("🔒 Collected CA certificate from %s for combined trust store\n", serverName)
				}
			}
		}
	}

	if len(combinedCAPEM) == 0 {
		return ""
	}

	// Write combined CA file
	caFile := filepath.Join(musterConfigPath, "mock-oauth-ca.pem")
	if err := os.WriteFile(caFile, combinedCAPEM, 0644); err != nil { //nolint:gosec
		if m.debug {
			logger.Debug("⚠️  Failed to write combined CA file: %v\n", err)
		}
		return ""
	}

	return caFile
}

// buildMusterOAuthServerConfig builds the OAuth server configuration for muster
// when using a mock OAuth server as the upstream identity provider.
func (m *musterInstanceManager) buildMusterOAuthServerConfig(
	oauthCfg MockOAuthServerConfig,
	port int,
	instanceID string,
	oauthProxyConfig map[string]interface{},
	logger TestLogger,
) map[string]interface{} {
	// Get the mock OAuth server info (should already be started)
	m.mu.RLock()
	oauthServers := m.mockOAuthServers[instanceID]
	m.mu.RUnlock()

	if oauthServers == nil {
		return nil
	}

	mockServer, exists := oauthServers[oauthCfg.Name]
	if !exists {
		return nil
	}

	issuerURL := mockServer.GetIssuerURL()

	// Build Dex config with the issuer URL
	dexConfig := map[string]interface{}{
		"issuerUrl":    issuerURL,
		"clientId":     oauthCfg.ClientID,
		"clientSecret": oauthCfg.ClientSecret,
	}

	// The combined CA bundle (written by configureOAuthForInstance) is passed
	// to `muster serve` via --extra-ca-file, which augments the process-wide
	// http.DefaultTransport. The Dex provider inherits that, so no per-config
	// caFile is required here.

	if m.debug {
		logger.Debug("🔐 Enabled muster OAuth server with mock provider (issuer: %s)\n", issuerURL)
	}

	return map[string]interface{}{
		keyEnabled:                      true,
		"baseUrl":                       fmt.Sprintf("http://localhost:%d", port),
		"provider":                      "dex", // Mock server acts like Dex
		"dex":                           dexConfig,
		"storage":                       map[string]interface{}{"type": "memory"},
		"allowLocalhostRedirectURIs":    true,
		"allowPublicClientRegistration": true, // Allow dynamic client registration for testing
	}
}

// generateConfigFilesWithMocks generates configuration files with mock HTTP server information
func (m *musterInstanceManager) generateConfigFilesWithMocks(configPath string, config *MusterPreConfiguration, port int, mockHTTPServers map[string]*MockHTTPServerInfo, instanceID string, logger TestLogger) error {
	// Create muster subdirectory - this is where muster serve will look for configs
	musterConfigPath := filepath.Join(configPath, "muster")

	// Validate that we're working with an absolute path to prevent directory creation in wrong location
	if !filepath.IsAbs(musterConfigPath) {
		return fmt.Errorf("muster config path is not absolute: %s", musterConfigPath)
	}

	// Only create the main muster config directory
	if err := os.MkdirAll(musterConfigPath, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create muster config directory: %w", err)
	}

	// Create mocks directory for mock configurations (only if needed)
	if config != nil && len(config.MCPServers) > 0 {
		if err := os.MkdirAll(filepath.Join(configPath, "mocks"), 0755); err != nil { //nolint:gosec
			return fmt.Errorf("failed to create mocks directory: %w", err)
		}
	}

	// Generate main config.yaml in muster subdirectory
	aggregatorConfig := map[string]interface{}{
		"host":      "localhost",
		"port":      port,
		"transport": "streamable-http",
		keyEnabled:  true,
	}

	// Configure OAuth if mock OAuth servers are defined
	if config != nil && len(config.MockOAuthServers) > 0 {
		m.configureOAuthForInstance(aggregatorConfig, config, port, instanceID, musterConfigPath, logger)
	}

	mainConfig := map[string]interface{}{
		"aggregator": aggregatorConfig,
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
	if err := m.writeYAMLFile(configFile, mainConfig, logger); err != nil {
		return fmt.Errorf("failed to write main config: %w", err)
	}

	if m.debug {
		// Show the generated config
		configContent, _ := os.ReadFile(configFile) //nolint:gosec
		logger.Debug("📋 Generated config.yaml:\n%s\n", string(configContent))
	}

	// Generate configuration files if config is provided
	if config != nil {
		// Generate MCP server CRDs for the new unified client
		if len(config.MCPServers) > 0 {
			// Create directory structure for CRDs: mcpservers/ (no namespace subdirectory)
			crdDir := filepath.Join(musterConfigPath, "mcpservers")
			if err := os.MkdirAll(crdDir, 0755); err != nil { //nolint:gosec
				return fmt.Errorf("failed to create MCPServer CRD directory %s: %w", crdDir, err)
			}

			for _, mcpServer := range config.MCPServers {
				tools, hasMockTools := mcpServer.Config["tools"]
				serverType, hasType := mcpServer.Config["type"].(string)

				// Check if this is a mock HTTP server (URL-based with tools)
				if hasMockTools && hasType && (serverType == "sse" || serverType == "streamable-http") && mockHTTPServers != nil {
					// Use the pre-started mock HTTP server
					mockInfo, exists := mockHTTPServers[mcpServer.Name]
					if !exists {
						return fmt.Errorf("mock HTTP server info not found for %s", mcpServer.Name)
					}

					// Create MCPServer CRD pointing to the mock HTTP server
					spec := map[string]interface{}{
						"type":      serverType,
						"autoStart": true,
						"url":       mockInfo.Endpoint,
					}

					if toolPrefix, ok := mcpServer.Config["toolPrefix"].(string); ok && toolPrefix != "" {
						spec["toolPrefix"] = toolPrefix
					}
					if family, ok := mcpServer.Config["family"].(map[string]interface{}); ok {
						spec["family"] = family
					}

					// Handle SSO configuration from oauth config
					if oauthConfig, hasOAuth := mcpServer.Config["oauth"].(map[string]interface{}); hasOAuth {
						authConfig := make(map[string]interface{})

						// If oauth.forward_token is specified, add auth.forwardToken to the CRD
						// This enables SSO token forwarding for this server
						if forwardToken, hasForwardToken := oauthConfig["forward_token"].(bool); hasForwardToken && forwardToken {
							authConfig["forwardToken"] = true
							if m.debug {
								logger.Debug("🔐 Enabling token forwarding for MCPServer %s\n", mcpServer.Name)
							}
						}

						// If oauth.token_exchange is specified, add auth.tokenExchange to the CRD
						// This enables SSO via RFC 8693 token exchange for cross-cluster SSO
						if tokenExchange, hasTokenExchange := oauthConfig["token_exchange"].(map[string]interface{}); hasTokenExchange {
							tokenExchangeConfig := map[string]interface{}{
								keyEnabled: true,
							}

							// Handle dex_token_endpoint - can be explicit URL or reference to OAuth server
							if dexEndpoint, ok := tokenExchange["dex_token_endpoint"].(string); ok {
								tokenExchangeConfig["dexTokenEndpoint"] = dexEndpoint
							} else if oauthServerRef, ok := tokenExchange["oauth_server_ref"].(string); ok {
								// Resolve token endpoint from referenced OAuth server
								m.mu.RLock()
								oauthServers := m.mockOAuthServers[instanceID]
								m.mu.RUnlock()
								if oauthServers != nil {
									if oauthServer, ok := oauthServers[oauthServerRef]; ok {
										tokenExchangeConfig["dexTokenEndpoint"] = oauthServer.GetIssuerURL() + "/token"
										if m.debug {
											logger.Debug("🔐 Resolved token exchange endpoint from %s: %s/token\n",
												oauthServerRef, oauthServer.GetIssuerURL())
										}
									}
								}
							}

							if connectorID, ok := tokenExchange["connector_id"].(string); ok {
								tokenExchangeConfig["connectorId"] = connectorID
							}
							if scopes, ok := tokenExchange["scopes"].(string); ok {
								tokenExchangeConfig["scopes"] = scopes
							}
							// Handle expected_issuer for proxied access scenarios
							// This allows tests to specify a different issuer than the access URL
							if expectedIssuer, ok := tokenExchange["expected_issuer"].(string); ok {
								tokenExchangeConfig["expectedIssuer"] = expectedIssuer
								if m.debug {
									logger.Debug("🔐 Using explicit expectedIssuer for %s: %s\n",
										mcpServer.Name, expectedIssuer)
								}
							}
							authConfig["tokenExchange"] = tokenExchangeConfig
							if m.debug {
								logger.Debug("🔐 Enabling token exchange for MCPServer %s (connector: %v)\n",
									mcpServer.Name, tokenExchange["connector_id"])
							}
						}

						if len(authConfig) > 0 {
							spec["auth"] = authConfig
						}
					}

					mcpServerCRD := map[string]interface{}{
						"apiVersion": "muster.giantswarm.io/v1alpha1",
						"kind":       "MCPServer",
						"metadata": map[string]interface{}{
							"name":      mcpServer.Name,
							"namespace": "default",
						},
						"spec": spec,
					}

					if m.debug {
						logger.Debug("🌐 MCPServer CRD for %s (HTTP mock): %+v\n", mcpServer.Name, mcpServerCRD)
					}

					// Save MCPServer CRD
					filename := filepath.Join(crdDir, mcpServer.Name+".yaml")
					if err := m.writeYAMLFile(filename, mcpServerCRD, logger); err != nil {
						return fmt.Errorf("failed to write MCPServer CRD %s: %w", mcpServer.Name, err)
					}

					if m.debug {
						logger.Debug("🌐 Created HTTP mock MCPServer CRD %s with %d tools (endpoint: %s)\n",
							mcpServer.Name, len(tools.([]interface{})), mockInfo.Endpoint)
					}
				} else if hasMockTools {
					// Stdio-based mock server (existing behavior)
					musterPath, err := m.getMusterBinaryPath()
					if err != nil {
						return fmt.Errorf("failed to get muster binary path: %w", err)
					}

					// Create MCPServer CRD for the unified client (filesystem mode)
					mockConfigFile := filepath.Join(configPath, "mocks", mcpServer.Name+".yaml")

					// Create MCPServer CRD structure
					stdioSpec := map[string]interface{}{
						"type":      "stdio",
						"autoStart": true,
						"command":   musterPath,
						"args":      []string{"test", "--mock-mcp-server", "--mock-config", mockConfigFile},
					}
					if toolPrefix, ok := mcpServer.Config["toolPrefix"].(string); ok && toolPrefix != "" {
						stdioSpec["toolPrefix"] = toolPrefix
					}
					if family, ok := mcpServer.Config["family"].(map[string]interface{}); ok {
						stdioSpec["family"] = family
					}
					mcpServerCRD := map[string]interface{}{
						"apiVersion": "muster.giantswarm.io/v1alpha1",
						"kind":       "MCPServer",
						"metadata": map[string]interface{}{
							"name":      mcpServer.Name,
							"namespace": "default",
						},
						"spec": stdioSpec,
					}

					if m.debug {
						logger.Debug("🧪 MCPServer CRD for %s: %+v\n", mcpServer.Name, mcpServerCRD)
						logger.Debug("🧪 Tools config for %s: %+v\n", mcpServer.Name, mcpServer.Config)
					}

					// Save MCPServer CRD (what the unified client reads)
					filename := filepath.Join(crdDir, mcpServer.Name+".yaml")
					if err := m.writeYAMLFile(filename, mcpServerCRD, logger); err != nil {
						return fmt.Errorf("failed to write MCPServer CRD %s: %w", mcpServer.Name, err)
					}

					// Save mock tools config to mocks directory (what mock server reads)
					if err := m.writeYAMLFile(mockConfigFile, mcpServer.Config, logger); err != nil {
						return fmt.Errorf("failed to write mock config %s: %w", mcpServer.Name, err)
					}

					if m.debug {
						logger.Debug("🧪 Created mock MCPServer CRD %s with %d tools\n", mcpServer.Name, len(tools.([]interface{})))
					}
				} else {
					// For regular servers, convert Config to MCPServer CRD format
					mcpServerCRD := map[string]interface{}{
						"apiVersion": "muster.giantswarm.io/v1alpha1",
						"kind":       "MCPServer",
						"metadata": map[string]interface{}{
							"name":      mcpServer.Name,
							"namespace": "default",
						},
						"spec": mcpServer.Config,
					}

					filename := filepath.Join(crdDir, mcpServer.Name+".yaml")
					if err := m.writeYAMLFile(filename, mcpServerCRD, logger); err != nil {
						return fmt.Errorf("failed to write MCPServer CRD %s: %w", mcpServer.Name, err)
					}
				}
			}
		}

		// Generate workflow CRDs in muster subdirectory (only if workflows exist)
		if len(config.Workflows) > 0 {
			// Create directory structure for CRDs: workflows/ (no namespace subdirectory)
			crdDir := filepath.Join(musterConfigPath, "workflows")
			if err := os.MkdirAll(crdDir, 0755); err != nil { //nolint:gosec
				return fmt.Errorf("failed to create Workflow CRD directory %s: %w", crdDir, err)
			}

			for _, workflow := range config.Workflows {
				// Create Workflow CRD structure with proper conversion
				metadata := map[string]interface{}{
					"name":      workflow.Name,
					"namespace": "default",
				}
				if len(workflow.Labels) > 0 {
					labels := make(map[string]interface{}, len(workflow.Labels))
					for k, v := range workflow.Labels {
						labels[k] = v
					}
					metadata["labels"] = labels
				}
				workflowCRD := map[string]interface{}{
					"apiVersion": "muster.giantswarm.io/v1alpha1",
					"kind":       "Workflow",
					"metadata":   metadata,
					"spec":       m.convertWorkflowConfigToCRDSpec(workflow.Config),
				}

				filename := filepath.Join(crdDir, workflow.Name+".yaml")
				if err := m.writeYAMLFile(filename, workflowCRD, logger); err != nil {
					return fmt.Errorf("failed to write Workflow CRD %s: %w", workflow.Name, err)
				}

				if m.debug {
					logger.Debug("📋 Created Workflow CRD %s\n", workflow.Name)
				}
			}
		}

		// Generate service configs in muster subdirectory (only if services exist)
		if len(config.Services) > 0 {
			// Create services directory only when needed
			servicesDir := filepath.Join(musterConfigPath, "services")
			if err := os.MkdirAll(servicesDir, 0755); err != nil { //nolint:gosec
				return fmt.Errorf("failed to create services directory: %w", err)
			}

			for _, service := range config.Services {
				filename := filepath.Join(servicesDir, service.Name+".yaml")
				if err := m.writeYAMLFile(filename, service.Config, logger); err != nil {
					return fmt.Errorf("failed to write service config %s: %w", service.Name, err)
				}
			}
		}
	}

	return nil
}

// extractExpectedToolsWithHTTPMocks extracts expected tool names from the configuration,
// including tools from HTTP mock servers.
// Note: OAuth-protected servers are excluded since their tools won't be available until authenticated.
func (m *musterInstanceManager) extractExpectedToolsWithHTTPMocks(config *MusterPreConfiguration, mockHTTPServers map[string]*MockHTTPServerInfo) []string {
	if config == nil {
		return []string{}
	}

	var expectedTools []string

	// Identify family groups whose members would fall back to per-server
	// prefixing: same family.name but diverging family.instanceArg, or same
	// (family.name, tool.name) with diverging descriptions. For those tools,
	// expect x_<server>_<tool> instead of x_<family.name>_<tool>.
	familyArgs := map[string]string{}       // family name -> first-seen instanceArg
	familyArgDivergent := map[string]bool{} // family name -> true when args disagree
	type toolKey struct{ family, name string }
	toolDescriptions := map[toolKey]string{}
	toolDivergent := map[toolKey]bool{}
	for _, mcpServer := range config.MCPServers {
		family, ok := mcpServer.Config["family"].(map[string]interface{})
		if !ok {
			continue
		}
		familyName, _ := family["name"].(string)
		if familyName == "" {
			continue
		}
		instanceArg, _ := family["instanceArg"].(string)
		if prev, seen := familyArgs[familyName]; seen && prev != instanceArg {
			familyArgDivergent[familyName] = true
		} else if !seen {
			familyArgs[familyName] = instanceArg
		}
		if tools, hasTools := mcpServer.Config["tools"]; hasTools {
			if toolsList, ok := tools.([]interface{}); ok {
				for _, tool := range toolsList {
					toolMap, ok := tool.(map[string]interface{})
					if !ok {
						continue
					}
					name, _ := toolMap["name"].(string)
					if name == "" {
						continue
					}
					desc, _ := toolMap["description"].(string)
					key := toolKey{family: familyName, name: name}
					if prev, seen := toolDescriptions[key]; seen && prev != desc {
						toolDivergent[key] = true
					} else if !seen {
						toolDescriptions[key] = desc
					}
				}
			}
		}
	}

	// Extract tools from MCP server configurations
	for _, mcpServer := range config.MCPServers {
		// For OAuth-protected servers, no tools are exposed until authenticated (per ADR-008)
		// Users must use core_auth_login to authenticate
		oauthConfig := m.extractOAuthConfig(mcpServer.Config)
		if oauthConfig != nil && oauthConfig.Required {
			// Per ADR-008: No synthetic auth tools - core_auth_login is always available
			if m.debug {
				m.logger.Debug("🔐 OAuth-protected server %s: no tools until authenticated (use core_auth_login)\n", mcpServer.Name)
			}
			continue
		}

		// Family-grouped servers expose tools as x_<family.name>_<tool>; non-
		// family servers retain per-server prefixing as x_<server>_<tool>.
		familyName := ""
		familyInstanceArg := ""
		if family, ok := mcpServer.Config["family"].(map[string]interface{}); ok {
			if name, ok := family["name"].(string); ok {
				familyName = name
			}
			if arg, ok := family["instanceArg"].(string); ok {
				familyInstanceArg = arg
			}
		}

		if tools, hasTools := mcpServer.Config["tools"]; hasTools {
			if toolsList, ok := tools.([]interface{}); ok {
				for _, tool := range toolsList {
					if toolMap, ok := tool.(map[string]interface{}); ok {
						if name, ok := toolMap["name"].(string); ok {
							var prefixedName string
							// instanceArg colliding with a declared tool
							// property causes the aggregator to fall back to
							// per-server prefixing for that tool. Mirror the
							// production logic here so readiness checks
							// expect the same exposed name.
							instanceArgCollides := false
							if familyInstanceArg != "" {
								if schema, ok := toolMap["input_schema"].(map[string]interface{}); ok {
									if props, ok := schema["properties"].(map[string]interface{}); ok {
										_, instanceArgCollides = props[familyInstanceArg]
									}
								}
							}
							grouped := familyName != "" &&
								!familyArgDivergent[familyName] &&
								!toolDivergent[toolKey{family: familyName, name: name}] &&
								!instanceArgCollides
							if grouped {
								prefixedName = fmt.Sprintf("x_%s_%s", familyName, name)
							} else {
								prefixedName = fmt.Sprintf("x_%s_%s", mcpServer.Name, name)
							}
							expectedTools = append(expectedTools, prefixedName)
						}
					}
				}
			}
		}
	}

	if m.debug && len(expectedTools) > 0 {
		m.logger.Debug("🎯 Extracted expected tools from configuration (including HTTP mocks): %v\n", expectedTools)
	}

	return expectedTools
}

// Cleanup cleans up all temporary directories created by this manager
func (m *musterInstanceManager) Cleanup() error {
	if m.tempDir != "" && !m.keepTempConfig {
		return os.RemoveAll(m.tempDir)
	}
	if m.keepTempConfig && m.debug {
		m.logger.Debug("🔍 Keeping temporary directory for debugging: %s\n", m.tempDir)
	}
	return nil
}

// extractExpectedMCPServers extracts all MCP server names from the configuration.
// This includes OAuth-protected servers that may be in "auth_required" state.
// The returned list is used by WaitForReady to ensure servers are registered before tests run.
func (m *musterInstanceManager) extractExpectedMCPServers(config *MusterPreConfiguration) []string {
	if config == nil {
		return []string{}
	}

	var expectedServers []string

	// Extract all MCP server names from configuration
	for _, mcpServer := range config.MCPServers {
		expectedServers = append(expectedServers, mcpServer.Name)
	}

	if m.debug && len(expectedServers) > 0 {
		m.logger.Debug("🎯 Extracted expected MCP servers from configuration: %v\n", expectedServers)
	}

	return expectedServers
}

// extractExpectedMCPServersFromInstance extracts expected MCP server names from instance configuration
func (m *musterInstanceManager) extractExpectedMCPServersFromInstance(instance *MusterInstance) []string {
	// Return the MCP servers stored during instance creation
	return instance.ExpectedMCPServers
}

// extractExpectedWorkflowsFromInstance extracts expected Workflow names from instance configuration.
// Currently returns empty as pre-configuration isn't stored with running instances.
// Tests that need to verify specific workflows should use explicit assertions in steps.
func (m *musterInstanceManager) extractExpectedWorkflowsFromInstance(_ *MusterInstance) []string {
	return []string{}
}

// checkMCPServersAvailability returns a map of server name to state for all
// registered servers, including servers in "Auth Required" state
// (OAuth-protected servers).
func (m *musterInstanceManager) checkMCPServersAvailability(client MCPTestClient, ctx context.Context) (map[string]string, error) {
	// showAll includes servers in "Failed" state, which core_mcpserver_list hides
	// by default; without it a failed server is indistinguishable from one that
	// has not registered yet, and the readiness loop waits out the full timeout
	// with no state to report.
	result, err := client.CallTool(ctx, "core_mcpserver_list", map[string]interface{}{"showAll": true})
	if err != nil {
		return nil, fmt.Errorf("failed to call core_mcpserver_list: %w", err)
	}

	serverStates := make(map[string]string)

	// Parse the response to extract server names and states.
	// The response structure is: {"mcpServers": [...]}
	jsonStr := ""

	// Method 1: Try reflection to access the Content field dynamically
	resultValue := reflect.ValueOf(result)
	if resultValue.Kind() == reflect.Pointer {
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
		var response map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &response); err == nil {
			if mcpServers, exists := response["mcpServers"]; exists {
				if serverArray, ok := mcpServers.([]interface{}); ok {
					for _, server := range serverArray {
						if serverMap, ok := server.(map[string]interface{}); ok {
							name, _ := serverMap["name"].(string)
							state, _ := serverMap["state"].(string)
							if name != "" {
								serverStates[name] = state
							}
						}
					}
				}
			}
		} else if m.debug {
			m.logger.Debug("🔍 Failed to parse JSON from core_mcpserver_list: %v, content: %s\n", err, jsonStr)
		}
	}

	return serverStates, nil
}

// mcpServerStateIsReady returns true when a server has reached a stable
// post-start state and tests can rely on it. Until then RegisterPendingAuth may
// not have run for OAuth-protected servers, causing core_auth_login to return
// "Server not found". This is an allowlist rather than a transient-state denylist:
// the reconciler writes "Disconnected"/"Stopped" for a registered-but-not-yet-started
// service, so a denylist would pass readiness inside the startup window, and any
// unknown future state fails closed (wait, then time out with the state visible)
// instead of silently counting as ready.
//
// Note: mock pre-configured servers hardcode autoStart=true and will always reach
// one of these states. Regular pre-configured servers without autoStart=true
// (autoStart defaults to false) will never start and readiness will time out.
func mcpServerStateIsReady(state string) bool {
	switch musterv1alpha1.MCPServerStateValue(state) {
	case musterv1alpha1.MCPServerStateRunning,
		musterv1alpha1.MCPServerStateConnected,
		musterv1alpha1.MCPServerStateAuthRequired:
		return true
	default:
		return false
	}
}

// findMissingMCPServers returns MCP servers that are expected but either absent
// or not yet in a ready state. Servers that are present but not ready are
// annotated with their current state so timeout reports show why they were
// not counted as ready.
func (m *musterInstanceManager) findMissingMCPServers(expectedServers []string, serverStates map[string]string) []string {
	var missing []string

	for _, expected := range expectedServers {
		state, found := serverStates[expected]
		switch {
		case !found:
			missing = append(missing, expected)
		case state == "":
			missing = append(missing, fmt.Sprintf("%s (no state reported)", expected))
		case !mcpServerStateIsReady(state):
			missing = append(missing, fmt.Sprintf("%s (state: %s)", expected, state))
		}
	}

	return missing
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

// convertWorkflowConfigToCRDSpec converts a raw Workflow config to a CRD spec format
// This handles the conversion of args fields in steps to RawExtension format
func (m *musterInstanceManager) convertWorkflowConfigToCRDSpec(config map[string]interface{}) map[string]interface{} {
	spec := make(map[string]interface{})

	if m.debug {
		m.logger.Debug("📋 Converting Workflow config with %d fields: %v\n", len(config), config)
	}

	// For test scenarios, we can use a simpler conversion that preserves the structure
	// without complex RawExtension processing which can cause stack overflow
	for key, value := range config {
		if m.debug {
			m.logger.Debug("📋 Processing field %s (type: %T): %v\n", key, value, value)
		}

		// Skip the name field as it should only be in metadata, not spec
		if key == "name" {
			if m.debug {
				m.logger.Debug("📋 Skipping name field from spec (should be in metadata only)\n")
			}
			continue
		}

		// Copy fields as-is for test scenarios
		// The actual CRD conversion with RawExtension happens in the workflow adapter
		spec[key] = value
	}

	if m.debug {
		m.logger.Debug("📋 Final converted workflow spec with %d fields: %v\n", len(spec), spec)
	}

	return spec
}
