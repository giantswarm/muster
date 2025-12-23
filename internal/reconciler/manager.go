package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/pkg/logging"
)

// Manager coordinates all reconciliation activities.
//
// It manages:
//   - Change detectors (filesystem/Kubernetes)
//   - Resource-specific reconcilers
//   - Work queue and worker pool
//   - Retry logic with exponential backoff
type Manager struct {
	mu sync.RWMutex

	config ManagerConfig

	// changeDetector detects configuration changes
	changeDetector ChangeDetector

	// reconcilers maps resource types to their reconcilers
	reconcilers map[ResourceType]Reconciler

	// queue is the work queue for reconciliation requests
	queue *delayedQueue

	// statusTracker tracks reconciliation status for each resource
	statusTracker map[string]*ReconcileStatus

	// changeChan receives change events from detectors
	changeChan chan ChangeEvent

	// ctx is the manager's context
	ctx context.Context

	// cancelFunc cancels the manager's context
	cancelFunc context.CancelFunc

	// wg tracks running workers
	wg sync.WaitGroup

	// running indicates if the manager is active
	running bool
}

// NewManager creates a new reconciliation manager.
func NewManager(config ManagerConfig) *Manager {
	// Apply defaults
	if config.WorkerCount == 0 {
		config.WorkerCount = 2
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 5
	}
	if config.InitialBackoff == 0 {
		config.InitialBackoff = time.Second
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = 5 * time.Minute
	}
	if config.DebounceInterval == 0 {
		config.DebounceInterval = 500 * time.Millisecond
	}

	return &Manager{
		config:        config,
		reconcilers:   make(map[ResourceType]Reconciler),
		queue:         NewDelayedQueue(),
		statusTracker: make(map[string]*ReconcileStatus),
		changeChan:    make(chan ChangeEvent, 100),
	}
}

// RegisterReconciler registers a reconciler for a specific resource type.
func (m *Manager) RegisterReconciler(reconciler Reconciler) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	resourceType := reconciler.GetResourceType()
	if _, exists := m.reconcilers[resourceType]; exists {
		return fmt.Errorf("reconciler for %s already registered", resourceType)
	}

	m.reconcilers[resourceType] = reconciler
	logging.Info("ReconcileManager", "Registered reconciler for %s", resourceType)

	// If detector is configured, add this resource type to watch
	if m.changeDetector != nil {
		if err := m.changeDetector.AddResourceType(resourceType); err != nil {
			logging.Warn("ReconcileManager", "Failed to add watch for %s: %v", resourceType, err)
		}
	}

	return nil
}

// Start begins the reconciliation system.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}

	m.ctx, m.cancelFunc = context.WithCancel(ctx)
	m.running = true

	// Create change detector based on mode
	if err := m.setupChangeDetector(); err != nil {
		m.running = false
		m.mu.Unlock()
		return fmt.Errorf("failed to setup change detector: %w", err)
	}

	// Add all registered resource types to the detector
	for resourceType := range m.reconcilers {
		if err := m.changeDetector.AddResourceType(resourceType); err != nil {
			logging.Warn("ReconcileManager", "Failed to add watch for %s: %v", resourceType, err)
		}
	}

	m.mu.Unlock()

	// Start change detector
	if err := m.changeDetector.Start(m.ctx, m.changeChan); err != nil {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
		return fmt.Errorf("failed to start change detector: %w", err)
	}

	// Start event processor
	m.wg.Add(1)
	go m.processChangeEvents()

	// Start workers
	for i := 0; i < m.config.WorkerCount; i++ {
		m.wg.Add(1)
		go m.worker(i)
	}

	logging.Info("ReconcileManager", "Started with %d workers", m.config.WorkerCount)
	return nil
}

// setupChangeDetector creates the appropriate change detector based on config.
func (m *Manager) setupChangeDetector() error {
	mode := m.config.Mode
	if mode == WatchModeAuto || mode == "" {
		// Auto-detect: use filesystem for now
		// TODO: Add Kubernetes detection when implemented
		mode = WatchModeFilesystem
	}

	switch mode {
	case WatchModeFilesystem:
		if m.config.FilesystemPath == "" {
			return fmt.Errorf("filesystem path required for filesystem mode")
		}
		m.changeDetector = NewFilesystemDetector(m.config.FilesystemPath, m.config.DebounceInterval)

	case WatchModeKubernetes:
		// TODO: Implement Kubernetes detector
		return fmt.Errorf("kubernetes mode not yet implemented")

	default:
		return fmt.Errorf("unknown watch mode: %s", mode)
	}

	return nil
}

// processChangeEvents converts change events to reconcile requests.
func (m *Manager) processChangeEvents() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return

		case event, ok := <-m.changeChan:
			if !ok {
				return
			}
			m.handleChangeEvent(event)
		}
	}
}

// handleChangeEvent processes a single change event.
func (m *Manager) handleChangeEvent(event ChangeEvent) {
	logging.Debug("ReconcileManager", "Handling change event: %s %s/%s",
		event.Operation, event.Type, event.Name)

	// Update status
	m.updateStatus(event.Type, event.Name, event.Namespace, StatePending, "")

	// Create reconcile request
	req := ReconcileRequest{
		Type:      event.Type,
		Name:      event.Name,
		Namespace: event.Namespace,
		Attempt:   1,
	}

	// Add to queue
	m.queue.Add(req)
}

// worker processes reconciliation requests from the queue.
func (m *Manager) worker(id int) {
	defer m.wg.Done()

	logging.Debug("ReconcileManager", "Worker %d started", id)

	for {
		req, ok := m.queue.Get(m.ctx)
		if !ok {
			logging.Debug("ReconcileManager", "Worker %d shutting down", id)
			return
		}

		m.processRequest(req)
		m.queue.Done(req)
	}
}

// processRequest handles a single reconciliation request.
func (m *Manager) processRequest(req ReconcileRequest) {
	m.mu.RLock()
	reconciler, ok := m.reconcilers[req.Type]
	m.mu.RUnlock()

	if !ok {
		logging.Warn("ReconcileManager", "No reconciler for resource type: %s", req.Type)
		return
	}

	// Update status to reconciling
	m.updateStatus(req.Type, req.Name, req.Namespace, StateReconciling, "")

	logging.Debug("ReconcileManager", "Reconciling %s/%s (attempt %d)",
		req.Type, req.Name, req.Attempt)

	// Execute reconciliation
	result := reconciler.Reconcile(m.ctx, req)

	// Handle result
	if result.Error != nil {
		m.handleReconcileError(req, result)
	} else if result.Requeue {
		m.handleRequeue(req, result)
	} else {
		m.handleSuccess(req)
	}
}

// handleReconcileError handles a failed reconciliation.
func (m *Manager) handleReconcileError(req ReconcileRequest, result ReconcileResult) {
	logging.Warn("ReconcileManager", "Reconciliation failed for %s/%s: %v",
		req.Type, req.Name, result.Error)

	// Check if we should retry
	if req.Attempt >= m.config.MaxRetries {
		logging.Error("ReconcileManager", result.Error,
			"Max retries exceeded for %s/%s", req.Type, req.Name)
		m.updateStatus(req.Type, req.Name, req.Namespace, StateFailed, result.Error.Error())
		return
	}

	// Update status
	m.updateStatus(req.Type, req.Name, req.Namespace, StateError, result.Error.Error())

	// Calculate backoff
	backoff := m.calculateBackoff(req.Attempt)

	// Requeue with backoff
	req.Attempt++
	req.LastError = result.Error
	m.queue.AddAfter(req, backoff)

	logging.Debug("ReconcileManager", "Requeuing %s/%s after %v (attempt %d)",
		req.Type, req.Name, backoff, req.Attempt)
}

// handleRequeue handles a successful reconciliation that needs requeueing.
func (m *Manager) handleRequeue(req ReconcileRequest, result ReconcileResult) {
	delay := result.RequeueAfter
	if delay == 0 {
		delay = m.config.InitialBackoff
	}

	m.queue.AddAfter(req, delay)
	logging.Debug("ReconcileManager", "Requeuing %s/%s after %v",
		req.Type, req.Name, delay)
}

// handleSuccess handles a successful reconciliation.
func (m *Manager) handleSuccess(req ReconcileRequest) {
	logging.Debug("ReconcileManager", "Successfully reconciled %s/%s", req.Type, req.Name)
	m.updateStatus(req.Type, req.Name, req.Namespace, StateSynced, "")
}

// calculateBackoff computes exponential backoff with jitter.
func (m *Manager) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initial * 2^attempt
	backoff := m.config.InitialBackoff * time.Duration(1<<uint(attempt-1))

	// Cap at max backoff
	if backoff > m.config.MaxBackoff {
		backoff = m.config.MaxBackoff
	}

	return backoff
}

// updateStatus updates the reconciliation status for a resource.
func (m *Manager) updateStatus(resourceType ResourceType, name, namespace string, state ReconcileState, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := statusKey(resourceType, name, namespace)
	status, ok := m.statusTracker[key]
	if !ok {
		status = &ReconcileStatus{
			ResourceType: resourceType,
			Name:         name,
			Namespace:    namespace,
		}
		m.statusTracker[key] = status
	}

	status.State = state
	status.LastError = errMsg

	if state == StateSynced {
		now := time.Now()
		status.LastReconcileTime = &now
		status.RetryCount = 0
	} else if state == StateError {
		status.RetryCount++
	}
}

// statusKey generates a unique key for status tracking.
func statusKey(resourceType ResourceType, name, namespace string) string {
	if namespace != "" {
		return string(resourceType) + "/" + namespace + "/" + name
	}
	return string(resourceType) + "/" + name
}

// Stop gracefully shuts down the reconciliation manager.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false
	m.mu.Unlock()

	logging.Info("ReconcileManager", "Stopping reconciliation manager...")

	// Cancel context
	if m.cancelFunc != nil {
		m.cancelFunc()
	}

	// Stop detector
	if m.changeDetector != nil {
		if err := m.changeDetector.Stop(); err != nil {
			logging.Error("ReconcileManager", err, "Error stopping change detector")
		}
	}

	// Shutdown queue
	m.queue.Shutdown()

	// Wait for workers
	m.wg.Wait()

	logging.Info("ReconcileManager", "Reconciliation manager stopped")
	return nil
}

// GetStatus returns the reconciliation status for a resource.
func (m *Manager) GetStatus(resourceType ResourceType, name, namespace string) (*ReconcileStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := statusKey(resourceType, name, namespace)
	status, ok := m.statusTracker[key]
	return status, ok
}

// GetAllStatuses returns all reconciliation statuses.
func (m *Manager) GetAllStatuses() []ReconcileStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]ReconcileStatus, 0, len(m.statusTracker))
	for _, status := range m.statusTracker {
		statuses = append(statuses, *status)
	}
	return statuses
}

// TriggerReconcile manually triggers reconciliation for a resource.
func (m *Manager) TriggerReconcile(resourceType ResourceType, name, namespace string) {
	event := ChangeEvent{
		Type:      resourceType,
		Name:      name,
		Namespace: namespace,
		Operation: OperationUpdate,
		Timestamp: time.Now(),
		Source:    SourceManual,
	}
	m.handleChangeEvent(event)
}

// IsRunning returns whether the manager is running.
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// GetQueueLength returns the current queue length.
func (m *Manager) GetQueueLength() int {
	return m.queue.Len()
}

