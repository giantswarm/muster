package teleport

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewCertWatcher(t *testing.T) {
	config := CertWatcherConfig{
		IdentityDir: "/tmp/test",
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	if watcher == nil {
		t.Fatal("Expected non-nil watcher")
	}

	// Check defaults were applied
	if watcher.config.WatchInterval != DefaultWatchInterval {
		t.Errorf("Expected WatchInterval to be %v, got %v", DefaultWatchInterval, watcher.config.WatchInterval)
	}
	if watcher.config.CertFile != DefaultCertFile {
		t.Errorf("Expected CertFile to be %s, got %s", DefaultCertFile, watcher.config.CertFile)
	}
	if watcher.config.KeyFile != DefaultKeyFile {
		t.Errorf("Expected KeyFile to be %s, got %s", DefaultKeyFile, watcher.config.KeyFile)
	}
	if watcher.config.CAFile != DefaultCAFile {
		t.Errorf("Expected CAFile to be %s, got %s", DefaultCAFile, watcher.config.CAFile)
	}
}

func TestCertWatcher_StartStop(t *testing.T) {
	dir := t.TempDir()

	// Create empty certificate files
	for _, f := range []string{DefaultCertFile, DefaultKeyFile, DefaultCAFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	config := CertWatcherConfig{
		IdentityDir: dir,
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	// Start should succeed
	if err := watcher.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !watcher.IsRunning() {
		t.Error("Expected watcher to be running")
	}

	// Starting again should be a no-op
	if err := watcher.Start(); err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}

	// Stop should succeed
	if err := watcher.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if watcher.IsRunning() {
		t.Error("Expected watcher to be stopped")
	}

	// Stopping again should be a no-op
	if err := watcher.Stop(); err != nil {
		t.Fatalf("Second Stop failed: %v", err)
	}
}

func TestCertWatcher_DetectsChanges(t *testing.T) {
	dir := t.TempDir()

	// Create initial certificate files
	for _, f := range []string{DefaultCertFile, DefaultKeyFile, DefaultCAFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("initial"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	var changeCount int32

	config := CertWatcherConfig{
		IdentityDir:   dir,
		WatchInterval: 50 * time.Millisecond, // Fast polling for test
		OnChange: func() {
			atomic.AddInt32(&changeCount, 1)
		},
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	if err := watcher.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer watcher.Stop()

	// Give the watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Modify a certificate file
	if err := os.WriteFile(filepath.Join(dir, DefaultCertFile), []byte("updated"), 0600); err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}

	// Wait for the change to be detected (debounce + polling interval)
	time.Sleep(700 * time.Millisecond)

	count := atomic.LoadInt32(&changeCount)
	if count < 1 {
		t.Errorf("Expected at least 1 change callback, got %d", count)
	}
}

func TestCertWatcher_DebounceMultipleChanges(t *testing.T) {
	dir := t.TempDir()

	// Create initial certificate files
	for _, f := range []string{DefaultCertFile, DefaultKeyFile, DefaultCAFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("initial"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	var changeCount int32

	config := CertWatcherConfig{
		IdentityDir:   dir,
		WatchInterval: 50 * time.Millisecond,
		OnChange: func() {
			atomic.AddInt32(&changeCount, 1)
		},
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	if err := watcher.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer watcher.Stop()

	// Give the watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Rapidly modify multiple certificate files
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(dir, DefaultCertFile), []byte("update"+string(rune('0'+i))), 0600); err != nil {
			t.Fatalf("Failed to update cert file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, DefaultKeyFile), []byte("update"+string(rune('0'+i))), 0600); err != nil {
			t.Fatalf("Failed to update key file: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for debounce to complete
	time.Sleep(800 * time.Millisecond)

	count := atomic.LoadInt32(&changeCount)
	// With debouncing, we should have fewer callbacks than file changes
	// The exact number depends on timing, but it should be 1-3 at most
	if count > 5 {
		t.Errorf("Expected debouncing to reduce callbacks, got %d", count)
	}
}

func TestCertWatcher_IsRelevantFile(t *testing.T) {
	config := CertWatcherConfig{
		IdentityDir: "/tmp",
		CertFile:    "client.crt",
		KeyFile:     "client.key",
		CAFile:      "ca.crt",
	}

	watcher, _ := NewCertWatcher(config)

	tests := []struct {
		fileName string
		expected bool
	}{
		{"client.crt", true},
		{"client.key", true},
		{"ca.crt", true},
		{"other.txt", false},
		{"tls.crt", false}, // Not matching custom cert file
		{"", false},
	}

	for _, test := range tests {
		t.Run(test.fileName, func(t *testing.T) {
			result := watcher.isRelevantFile(test.fileName)
			if result != test.expected {
				t.Errorf("isRelevantFile(%q) = %v, expected %v", test.fileName, result, test.expected)
			}
		})
	}
}

func TestCertWatcher_PollingFallback(t *testing.T) {
	dir := t.TempDir()

	// Create initial certificate files
	for _, f := range []string{DefaultCertFile, DefaultKeyFile, DefaultCAFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("initial"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	var changeCount int32

	config := CertWatcherConfig{
		IdentityDir:   dir,
		WatchInterval: 50 * time.Millisecond,
		OnChange: func() {
			atomic.AddInt32(&changeCount, 1)
		},
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	// Start the watcher
	if err := watcher.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer watcher.Stop()

	// Wait a bit to ensure the initial modtimes are captured
	time.Sleep(100 * time.Millisecond)

	// Modify a file
	time.Sleep(10 * time.Millisecond) // Ensure different modtime
	if err := os.WriteFile(filepath.Join(dir, DefaultCertFile), []byte("updated"), 0600); err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}

	// Wait for polling to detect the change
	time.Sleep(700 * time.Millisecond)

	count := atomic.LoadInt32(&changeCount)
	if count < 1 {
		t.Errorf("Expected at least 1 change callback from polling, got %d", count)
	}
}

func TestCertWatcher_UpdateModTimes(t *testing.T) {
	dir := t.TempDir()

	// Create certificate files
	for _, f := range []string{DefaultCertFile, DefaultKeyFile, DefaultCAFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	config := CertWatcherConfig{
		IdentityDir: dir,
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	// Manually call updateModTimes
	watcher.updateModTimes()

	// Check that modtimes were recorded
	if len(watcher.lastModTimes) != 3 {
		t.Errorf("Expected 3 modtimes, got %d", len(watcher.lastModTimes))
	}
}

func TestCertWatcher_CheckForChanges(t *testing.T) {
	dir := t.TempDir()

	// Create certificate files
	for _, f := range []string{DefaultCertFile, DefaultKeyFile, DefaultCAFile} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	config := CertWatcherConfig{
		IdentityDir: dir,
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	// Initialize modtimes
	watcher.updateModTimes()

	// No changes yet
	changed := watcher.checkForChanges()
	if changed {
		t.Error("Expected no changes initially")
	}

	// Modify a file
	time.Sleep(10 * time.Millisecond) // Ensure different modtime
	if err := os.WriteFile(filepath.Join(dir, DefaultCertFile), []byte("updated"), 0600); err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}

	// Now there should be changes
	changed = watcher.checkForChanges()
	if !changed {
		t.Error("Expected changes after file modification")
	}

	// After checkForChanges, modtimes are updated, so no changes on next call
	changed = watcher.checkForChanges()
	if changed {
		t.Error("Expected no changes after modtimes were updated")
	}
}

func TestCertWatcher_CheckForChanges_MissingFile(t *testing.T) {
	dir := t.TempDir()

	// Create only cert file
	if err := os.WriteFile(filepath.Join(dir, DefaultCertFile), []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	config := CertWatcherConfig{
		IdentityDir: dir,
	}

	watcher, err := NewCertWatcher(config)
	if err != nil {
		t.Fatalf("NewCertWatcher failed: %v", err)
	}

	// Initialize modtimes
	watcher.updateModTimes()

	// Should handle missing files gracefully
	changed := watcher.checkForChanges()
	if changed {
		t.Error("Expected no changes when files are missing")
	}
}
