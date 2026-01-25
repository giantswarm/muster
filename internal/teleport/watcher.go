package teleport

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"muster/pkg/logging"
)

// CertWatcherConfig holds configuration for the certificate watcher.
type CertWatcherConfig struct {
	// IdentityDir is the directory containing certificate files.
	IdentityDir string

	// CertFile is the certificate file name.
	CertFile string

	// KeyFile is the private key file name.
	KeyFile string

	// CAFile is the CA certificate file name.
	CAFile string

	// WatchInterval is the fallback polling interval when fsnotify is not available.
	WatchInterval time.Duration

	// OnChange is called when certificate files change.
	OnChange func()
}

// CertWatcher monitors certificate files for changes and triggers reloads.
// It uses fsnotify for efficient file system monitoring with a fallback to
// polling for environments where fsnotify is not available or reliable.
type CertWatcher struct {
	mu sync.Mutex

	config CertWatcherConfig

	// fsWatcher is the fsnotify watcher (may be nil if fsnotify is unavailable)
	fsWatcher *fsnotify.Watcher

	// stopCh signals the watcher to stop
	stopCh chan struct{}

	// running indicates if the watcher is active
	running bool

	// lastModTimes tracks the last modification times for fallback polling
	lastModTimes map[string]time.Time

	// debounceTimer helps prevent rapid successive reloads
	debounceTimer *time.Timer
	debounceMu    sync.Mutex
}

// DefaultDebounceInterval is the time to wait before triggering a reload
// after the last file change is detected.
const DefaultDebounceInterval = 500 * time.Millisecond

// NewCertWatcher creates a new certificate watcher.
func NewCertWatcher(config CertWatcherConfig) (*CertWatcher, error) {
	if config.WatchInterval == 0 {
		config.WatchInterval = DefaultWatchInterval
	}
	if config.CertFile == "" {
		config.CertFile = DefaultCertFile
	}
	if config.KeyFile == "" {
		config.KeyFile = DefaultKeyFile
	}
	if config.CAFile == "" {
		config.CAFile = DefaultCAFile
	}

	return &CertWatcher{
		config:       config,
		lastModTimes: make(map[string]time.Time),
	}, nil
}

// Start begins watching for certificate changes.
func (w *CertWatcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return nil
	}

	w.stopCh = make(chan struct{})
	w.running = true

	// Try to use fsnotify for efficient file watching
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logging.Warn("CertWatcher", "fsnotify not available, falling back to polling: %v", err)
		// Fall back to polling
		go w.pollForChanges()
		return nil
	}

	w.fsWatcher = watcher

	// Add the identity directory to the watcher
	if err := w.fsWatcher.Add(w.config.IdentityDir); err != nil {
		logging.Warn("CertWatcher", "Failed to watch directory %s, falling back to polling: %v",
			w.config.IdentityDir, err)
		w.fsWatcher.Close()
		w.fsWatcher = nil
		go w.pollForChanges()
		return nil
	}

	// Capture channels before releasing lock to avoid race conditions
	eventsCh := w.fsWatcher.Events
	errorsCh := w.fsWatcher.Errors

	// Start the fsnotify event processor
	go w.processEvents(eventsCh, errorsCh)

	logging.Info("CertWatcher", "Started watching %s for certificate changes", w.config.IdentityDir)
	return nil
}

// processEvents handles fsnotify events.
// The channels are passed as parameters to avoid race conditions with Stop().
func (w *CertWatcher) processEvents(eventsCh <-chan fsnotify.Event, errorsCh <-chan error) {
	for {
		select {
		case <-w.stopCh:
			return

		case event, ok := <-eventsCh:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-errorsCh:
			if !ok {
				return
			}
			logging.Error("CertWatcher", err, "fsnotify error")
		}
	}
}

// handleEvent processes a single fsnotify event.
func (w *CertWatcher) handleEvent(event fsnotify.Event) {
	// Only handle relevant files
	fileName := filepath.Base(event.Name)
	if !w.isRelevantFile(fileName) {
		return
	}

	// Only handle write and create events
	if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		return
	}

	logging.Debug("CertWatcher", "Certificate file changed: %s", event.Name)

	// Debounce the reload
	w.triggerReloadDebounced()
}

// isRelevantFile checks if a filename is one of the certificate files we care about.
func (w *CertWatcher) isRelevantFile(fileName string) bool {
	return fileName == w.config.CertFile ||
		fileName == w.config.KeyFile ||
		fileName == w.config.CAFile
}

// triggerReloadDebounced triggers a reload after a debounce period.
// This prevents multiple rapid reloads when several files change at once
// (e.g., when tbot rotates all certificates simultaneously).
func (w *CertWatcher) triggerReloadDebounced() {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	// Cancel existing timer if present
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}

	// Start new debounce timer
	w.debounceTimer = time.AfterFunc(DefaultDebounceInterval, func() {
		w.mu.Lock()
		running := w.running
		callback := w.config.OnChange
		w.mu.Unlock()

		if running && callback != nil {
			callback()
		}
	})
}

// pollForChanges implements fallback polling when fsnotify is not available.
func (w *CertWatcher) pollForChanges() {
	ticker := time.NewTicker(w.config.WatchInterval)
	defer ticker.Stop()

	// Initialize last modification times
	w.updateModTimes()

	for {
		select {
		case <-w.stopCh:
			return

		case <-ticker.C:
			if w.checkForChanges() {
				logging.Debug("CertWatcher", "Certificate file changes detected via polling")
				w.triggerReloadDebounced()
			}
		}
	}
}

// updateModTimes updates the stored modification times for all certificate files.
func (w *CertWatcher) updateModTimes() {
	files := []string{
		filepath.Join(w.config.IdentityDir, w.config.CertFile),
		filepath.Join(w.config.IdentityDir, w.config.KeyFile),
		filepath.Join(w.config.IdentityDir, w.config.CAFile),
	}

	for _, file := range files {
		if info, err := os.Stat(file); err == nil {
			w.lastModTimes[file] = info.ModTime()
		}
	}
}

// checkForChanges checks if any certificate files have been modified.
func (w *CertWatcher) checkForChanges() bool {
	changed := false

	files := []string{
		filepath.Join(w.config.IdentityDir, w.config.CertFile),
		filepath.Join(w.config.IdentityDir, w.config.KeyFile),
		filepath.Join(w.config.IdentityDir, w.config.CAFile),
	}

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}

		currentModTime := info.ModTime()
		if lastModTime, exists := w.lastModTimes[file]; exists {
			if currentModTime.After(lastModTime) {
				changed = true
			}
		}
		w.lastModTimes[file] = currentModTime
	}

	return changed
}

// Stop gracefully stops the certificate watcher.
func (w *CertWatcher) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running {
		return nil
	}

	w.running = false
	close(w.stopCh)

	// Cancel any pending debounce timer
	w.debounceMu.Lock()
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
		w.debounceTimer = nil
	}
	w.debounceMu.Unlock()

	// Close fsnotify watcher if present
	if w.fsWatcher != nil {
		if err := w.fsWatcher.Close(); err != nil {
			logging.Warn("CertWatcher", "Error closing fsnotify watcher: %v", err)
		}
		w.fsWatcher = nil
	}

	logging.Info("CertWatcher", "Stopped certificate watcher")
	return nil
}

// IsRunning returns whether the watcher is currently active.
func (w *CertWatcher) IsRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}
