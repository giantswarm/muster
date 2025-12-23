package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"muster/pkg/logging"
)

// resourceDirMapping maps resource types to their directory names.
var resourceDirMapping = map[ResourceType]string{
	ResourceTypeMCPServer:     "mcpservers",
	ResourceTypeServiceClass:  "serviceclasses",
	ResourceTypeWorkflow:      "workflows",
}

// FilesystemDetector implements ChangeDetector for filesystem-based configurations.
//
// It uses fsnotify to watch for changes in YAML configuration files and
// generates change events when files are created, modified, or deleted.
type FilesystemDetector struct {
	mu sync.RWMutex

	// basePath is the root directory for configuration files
	basePath string

	// watcher is the fsnotify watcher instance
	watcher *fsnotify.Watcher

	// resourceTypes is the set of resource types being watched
	resourceTypes map[ResourceType]bool

	// debounceInterval is how long to wait for additional changes
	debounceInterval time.Duration

	// pendingEvents tracks pending debounced events
	pendingEvents map[string]*debounceEntry

	// stopCh signals shutdown
	stopCh chan struct{}

	// running indicates if the detector is active
	running bool
}

// debounceEntry tracks a pending event for debouncing.
type debounceEntry struct {
	event     ChangeEvent
	timer     *time.Timer
	operation ChangeOperation
}

// NewFilesystemDetector creates a new filesystem change detector.
func NewFilesystemDetector(basePath string, debounceInterval time.Duration) *FilesystemDetector {
	if debounceInterval == 0 {
		debounceInterval = 500 * time.Millisecond
	}

	return &FilesystemDetector{
		basePath:         basePath,
		resourceTypes:    make(map[ResourceType]bool),
		debounceInterval: debounceInterval,
		pendingEvents:    make(map[string]*debounceEntry),
		stopCh:           make(chan struct{}),
	}
}

// Start begins watching for filesystem changes.
func (d *FilesystemDetector) Start(ctx context.Context, changes chan<- ChangeEvent) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		d.mu.Unlock()
		return err
	}

	d.watcher = watcher
	d.running = true
	d.stopCh = make(chan struct{})
	d.mu.Unlock()

	// Add watches for all registered resource types
	if err := d.setupWatches(); err != nil {
		d.Stop()
		return err
	}

	// Start the event processing loop
	go d.processEvents(ctx, changes)

	logging.Info("FilesystemDetector", "Started watching %s for configuration changes", d.basePath)
	return nil
}

// setupWatches adds filesystem watches for all registered resource types.
func (d *FilesystemDetector) setupWatches() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for resourceType := range d.resourceTypes {
		if err := d.addWatchForType(resourceType); err != nil {
			logging.Warn("FilesystemDetector", "Failed to add watch for %s: %v", resourceType, err)
			// Continue with other types
		}
	}

	return nil
}

// addWatchForType adds a watch for a specific resource type directory.
func (d *FilesystemDetector) addWatchForType(resourceType ResourceType) error {
	dirName, ok := resourceDirMapping[resourceType]
	if !ok {
		return nil
	}

	watchPath := filepath.Join(d.basePath, dirName)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(watchPath, 0755); err != nil {
		return err
	}

	// Add the watch
	if err := d.watcher.Add(watchPath); err != nil {
		return err
	}

	logging.Debug("FilesystemDetector", "Watching directory: %s", watchPath)
	return nil
}

// processEvents handles filesystem events and generates change events.
func (d *FilesystemDetector) processEvents(ctx context.Context, changes chan<- ChangeEvent) {
	for {
		select {
		case <-ctx.Done():
			d.cleanupPendingEvents()
			return

		case <-d.stopCh:
			d.cleanupPendingEvents()
			return

		case event, ok := <-d.watcher.Events:
			if !ok {
				return
			}
			d.handleFsEvent(event, changes)

		case err, ok := <-d.watcher.Errors:
			if !ok {
				return
			}
			logging.Error("FilesystemDetector", err, "Filesystem watcher error")
		}
	}
}

// handleFsEvent processes a single filesystem event.
func (d *FilesystemDetector) handleFsEvent(event fsnotify.Event, changes chan<- ChangeEvent) {
	// Only handle YAML files
	if !isYAMLFile(event.Name) {
		return
	}

	// Determine the resource type from the path
	resourceType, name := d.parseFilePath(event.Name)
	if resourceType == "" {
		return
	}

	// Check if we're watching this resource type
	d.mu.RLock()
	watching := d.resourceTypes[resourceType]
	d.mu.RUnlock()
	if !watching {
		return
	}

	// Determine the operation
	var operation ChangeOperation
	switch {
	case event.Op&fsnotify.Create == fsnotify.Create:
		operation = OperationCreate
	case event.Op&fsnotify.Write == fsnotify.Write:
		operation = OperationUpdate
	case event.Op&fsnotify.Remove == fsnotify.Remove:
		operation = OperationDelete
	case event.Op&fsnotify.Rename == fsnotify.Rename:
		// Rename is treated as delete (the new name will trigger a create)
		operation = OperationDelete
	default:
		return
	}

	// Create the change event
	changeEvent := ChangeEvent{
		Type:      resourceType,
		Name:      name,
		Operation: operation,
		Timestamp: time.Now(),
		Source:    SourceFilesystem,
		FilePath:  event.Name,
	}

	// Debounce the event
	d.debounceEvent(changeEvent, changes)
}

// debounceEvent implements event debouncing to handle rapid successive changes.
func (d *FilesystemDetector) debounceEvent(event ChangeEvent, changes chan<- ChangeEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := string(event.Type) + "/" + event.Name

	// Cancel existing timer if present
	if entry, ok := d.pendingEvents[key]; ok {
		entry.timer.Stop()

		// Merge operations: Create + Update = Create, Update + Delete = Delete, etc.
		event.Operation = mergeOperations(entry.operation, event.Operation)
	}

	// Create new timer
	timer := time.AfterFunc(d.debounceInterval, func() {
		d.mu.Lock()
		entry, ok := d.pendingEvents[key]
		if ok {
			delete(d.pendingEvents, key)
		}
		d.mu.Unlock()

		if ok {
			select {
			case changes <- entry.event:
				logging.Debug("FilesystemDetector", "Emitted change event: %s %s/%s",
					entry.event.Operation, entry.event.Type, entry.event.Name)
			default:
				logging.Warn("FilesystemDetector", "Change event channel full, dropping event for %s/%s",
					entry.event.Type, entry.event.Name)
			}
		}
	})

	d.pendingEvents[key] = &debounceEntry{
		event:     event,
		timer:     timer,
		operation: event.Operation,
	}
}

// mergeOperations merges two operations into a single logical operation.
func mergeOperations(old, new ChangeOperation) ChangeOperation {
	// Create followed by anything stays as the final operation
	if old == OperationCreate {
		if new == OperationDelete {
			// Create + Delete = no-op, but we still emit Delete to clean up
			return OperationDelete
		}
		// Create + Update = Create
		return OperationCreate
	}

	// Update followed by Delete = Delete
	if old == OperationUpdate && new == OperationDelete {
		return OperationDelete
	}

	// Otherwise use the new operation
	return new
}

// parseFilePath extracts the resource type and name from a file path.
func (d *FilesystemDetector) parseFilePath(path string) (ResourceType, string) {
	// Get relative path from base
	relPath, err := filepath.Rel(d.basePath, path)
	if err != nil {
		return "", ""
	}

	// Split into components
	parts := strings.Split(relPath, string(filepath.Separator))
	if len(parts) < 2 {
		return "", ""
	}

	// First part should be the resource type directory
	dirName := parts[0]
	fileName := parts[len(parts)-1]

	// Map directory to resource type
	var resourceType ResourceType
	for rt, dn := range resourceDirMapping {
		if dn == dirName {
			resourceType = rt
			break
		}
	}

	if resourceType == "" {
		return "", ""
	}

	// Extract name from filename (remove .yaml or .yml extension)
	name := strings.TrimSuffix(fileName, ".yaml")
	name = strings.TrimSuffix(name, ".yml")

	return resourceType, name
}

// cleanupPendingEvents cancels all pending debounce timers.
func (d *FilesystemDetector) cleanupPendingEvents() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, entry := range d.pendingEvents {
		entry.timer.Stop()
	}
	d.pendingEvents = make(map[string]*debounceEntry)
}

// Stop gracefully stops the filesystem detector.
func (d *FilesystemDetector) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return nil
	}

	d.running = false
	close(d.stopCh)

	if d.watcher != nil {
		if err := d.watcher.Close(); err != nil {
			logging.Error("FilesystemDetector", err, "Error closing filesystem watcher")
		}
		d.watcher = nil
	}

	logging.Info("FilesystemDetector", "Stopped filesystem detector")
	return nil
}

// GetSource returns the change source type.
func (d *FilesystemDetector) GetSource() ChangeSource {
	return SourceFilesystem
}

// AddResourceType adds a resource type to watch.
func (d *FilesystemDetector) AddResourceType(resourceType ResourceType) error {
	d.mu.Lock()
	d.resourceTypes[resourceType] = true
	running := d.running
	d.mu.Unlock()

	// If already running, add the watch immediately
	if running {
		return d.addWatchForType(resourceType)
	}

	return nil
}

// RemoveResourceType removes a resource type from watching.
func (d *FilesystemDetector) RemoveResourceType(resourceType ResourceType) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.resourceTypes, resourceType)

	// Note: fsnotify doesn't support removing individual watches easily,
	// but since we check resourceTypes before processing events, this is fine

	return nil
}

// isYAMLFile checks if a file path is a YAML file.
func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

