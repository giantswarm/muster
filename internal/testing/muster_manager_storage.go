package testing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
)

// Storage backends a scenario can run its muster serve instance on.
const (
	// StorageMemory is the default: every store dies with the process.
	StorageMemory = "memory"
	// StorageValkey runs the instance on an in-process Valkey stand-in
	// (miniredis) that outlives the process the way an installation's
	// Valkey outlives a pod. Every store muster keeps outside the process --
	// session auth, capabilities, OAuth tokens, state, client credentials and
	// the OAuth server's own store -- lands there.
	StorageValkey = "valkey"
)

// instanceValkey is the Valkey stand-in an instance with storage type valkey
// runs on: one miniredis per instance, on a port from the harness's
// allocator, kept across test_restart_instance so the stores survive.
type instanceValkey struct {
	srv  *miniredis.Miniredis
	port int

	// commands counts the commands the stand-in has dispatched, by command
	// name, across restarts; test_measure_meta_tool reads it around a call to
	// say what the call cost the store.
	commandsMu sync.Mutex
	commands   map[string]int

	// started is closed once the (possibly delayed) first start has run;
	// startErr then holds its result. A scenario that declares start_delay
	// has the port answering ECONNREFUSED until then, the way a Valkey pod
	// that is not scheduled yet does.
	started  chan struct{}
	startErr error
	once     sync.Once
}

// addr is the address muster is configured with.
func (v *instanceValkey) addr() string {
	return fmt.Sprintf("127.0.0.1:%d", v.port)
}

// start binds the miniredis to its reserved port. Runs at most once; later
// stops and starts go through StopValkey / StartValkey.
func (v *instanceValkey) start() error {
	v.once.Do(func() {
		v.startErr = v.srv.StartAddr(v.addr())
		if v.startErr == nil {
			v.installHooks()
		}
		close(v.started)
	})
	return v.startErr
}

// installHooks installs the stand-in's pre-dispatch hook: the CLIENT TRACKING
// compatibility answer and the per-command count. miniredis builds a fresh
// server on every start, so this runs after each one.
func (v *instanceValkey) installHooks() {
	v.srv.Server().SetPreHook(func(c *server.Peer, cmd string, args ...string) bool {
		v.countCommand(cmd, args)
		return answerClientTracking(c, cmd, args)
	})
}

// countCommand records one dispatched command under its upper-case name
// (CLIENT TRACKING under "CLIENT TRACKING").
func (v *instanceValkey) countCommand(cmd string, args []string) {
	name := strings.ToUpper(cmd)
	if name == "CLIENT" && len(args) > 0 {
		name += " " + strings.ToUpper(args[0])
	}
	v.commandsMu.Lock()
	if v.commands == nil {
		v.commands = map[string]int{}
	}
	v.commands[name]++
	v.commandsMu.Unlock()
}

// commandCounts returns a copy of the per-command counts so far.
func (v *instanceValkey) commandCounts() map[string]int {
	v.commandsMu.Lock()
	defer v.commandsMu.Unlock()
	out := make(map[string]int, len(v.commands))
	for k, n := range v.commands {
		out[k] = n
	}
	return out
}

// answerClientTracking makes the stand-in accept the one command a valkey-go
// client sends on connect that miniredis does not implement: CLIENT TRACKING,
// the client-side-cache handshake. Answering it with OK is what a Valkey
// without the feature in use does for a client that never reads through the
// cache; refusing it would fail every client created without DisableCache --
// among them the muster releases before v5.19.13, which the harness runs as
// regression baselines. Reports whether it handled the command.
func answerClientTracking(c *server.Peer, cmd string, args []string) bool {
	if cmd == "CLIENT" && len(args) > 0 && strings.EqualFold(args[0], "TRACKING") {
		c.WriteOK()
		return true
	}
	return false
}

// awaitStart blocks until the first start has run or ctx ends, and reports
// the start's result.
func (v *instanceValkey) awaitStart(ctx context.Context) error {
	select {
	case <-v.started:
		return v.startErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// storageType reports the backend a pre-configuration asks for, defaulting
// to memory.
func storageType(config *MusterPreConfiguration) string {
	if config == nil || config.Storage == nil || config.Storage.Type == "" {
		return StorageMemory
	}
	return strings.ToLower(config.Storage.Type)
}

// validateStorageConfig rejects a storage block the harness cannot honour, at
// load time, so a typo does not run a scenario on memory while it reads as
// Valkey.
func validateStorageConfig(config *MusterPreConfiguration) error {
	if config == nil || config.Storage == nil {
		return nil
	}
	switch storageType(config) {
	case StorageMemory, StorageValkey:
	default:
		return fmt.Errorf("pre_configuration.storage.type must be %q or %q, got %q", StorageMemory, StorageValkey, config.Storage.Type)
	}
	if config.Storage.StartDelay < 0 {
		return fmt.Errorf("pre_configuration.storage.start_delay must not be negative, got %s", config.Storage.StartDelay)
	}
	if config.Storage.StartDelay > 0 && storageType(config) != StorageValkey {
		return fmt.Errorf("pre_configuration.storage.start_delay needs storage.type: valkey")
	}
	return nil
}

// startValkey starts the instance's Valkey stand-in when the scenario asks
// for one. The port comes from the harness's allocator so parallel instances
// and a second harness on the same base port cannot collide on it; the probe
// listener is released at once, so until the store starts a connect is
// refused rather than accepted and left unanswered. With start_delay the
// store starts that long after this call returns, which is after muster
// serve has been started (CreateInstance runs this before the process).
func (m *musterInstanceManager) startValkey(ctx context.Context, instanceID string, config *MusterPreConfiguration, logger TestLogger) error {
	if storageType(config) != StorageValkey {
		return nil
	}
	port, err := m.findAvailablePort(instanceID, logger)
	if err != nil {
		return fmt.Errorf("failed to find available port for valkey: %w", err)
	}
	m.closeReservedListener(port)

	v := &instanceValkey{
		srv:     miniredis.NewMiniRedis(),
		port:    port,
		started: make(chan struct{}),
	}
	m.mu.Lock()
	m.valkeys[instanceID] = v
	m.mu.Unlock()

	delay := config.Storage.StartDelay
	if delay <= 0 {
		if err := v.start(); err != nil {
			m.stopValkey(instanceID, logger)
			return fmt.Errorf("failed to start valkey on %s: %w", v.addr(), err)
		}
		if m.debug {
			logger.Debug("🗄️  Started valkey (miniredis) for %s on %s\n", instanceID, v.addr())
		}
		return nil
	}

	if m.debug {
		logger.Debug("🗄️  Valkey (miniredis) for %s will answer on %s after %s\n", instanceID, v.addr(), delay)
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			v.once.Do(func() {
				v.startErr = ctx.Err()
				close(v.started)
			})
			return
		case <-timer.C:
		}
		if err := v.start(); err != nil {
			logger.Debug("⚠️  Delayed valkey start for %s failed: %v\n", instanceID, err)
		} else if m.debug {
			logger.Debug("🗄️  Valkey (miniredis) for %s now answers on %s\n", instanceID, v.addr())
		}
	}()
	return nil
}

// valkeyFor returns the instance's Valkey stand-in, or nil when the instance
// runs on memory.
func (m *musterInstanceManager) valkeyFor(instanceID string) *instanceValkey {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.valkeys[instanceID]
}

// stopValkey shuts the instance's Valkey stand-in down for good and hands its
// port back. Part of DestroyInstance; a mid-scenario outage is StopValkey.
func (m *musterInstanceManager) stopValkey(instanceID string, logger TestLogger) {
	m.mu.Lock()
	v, exists := m.valkeys[instanceID]
	if exists {
		delete(m.valkeys, instanceID)
	}
	m.mu.Unlock()
	if !exists {
		return
	}
	v.srv.Close()
	m.releasePort(v.port, instanceID, logger)
	if m.debug {
		logger.Debug("🗄️  Stopped valkey (miniredis) for %s\n", instanceID)
	}
}

// StopValkey takes the instance's Valkey stand-in off its port while keeping
// its data, the outage of a Valkey pod being rescheduled. StartValkey brings
// it back on the same port with the same data.
func (m *musterInstanceManager) StopValkey(instanceID string) error {
	v := m.valkeyFor(instanceID)
	if v == nil {
		return fmt.Errorf("instance %s runs on %s storage; declare pre_configuration.storage.type: %s", instanceID, StorageMemory, StorageValkey)
	}
	v.srv.Close()
	return nil
}

// StartValkey brings a stopped Valkey stand-in back on its port with its data
// intact. Starting one that never started yet (start_delay still pending)
// starts it now.
func (m *musterInstanceManager) StartValkey(instanceID string) error {
	v := m.valkeyFor(instanceID)
	if v == nil {
		return fmt.Errorf("instance %s runs on %s storage; declare pre_configuration.storage.type: %s", instanceID, StorageMemory, StorageValkey)
	}
	select {
	case <-v.started:
	default:
		return v.start()
	}
	if err := v.srv.Restart(); err != nil {
		return fmt.Errorf("failed to restart valkey on %s: %w", v.addr(), err)
	}
	v.installHooks()
	return nil
}

// applyStorageConfig points muster's OAuth server storage -- the block every
// backed store reads its backend from -- at the instance's Valkey stand-in.
// A memory instance leaves the configuration alone. The block lives under
// aggregator.oauth.server whether or not the OAuth server itself is enabled:
// the aggregator's session stores read it regardless.
func (m *musterInstanceManager) applyStorageConfig(aggregatorConfig map[string]interface{}, instanceID string) {
	v := m.valkeyFor(instanceID)
	if v == nil {
		return
	}
	oauthConfig := ensureStringMap(aggregatorConfig, "oauth")
	serverConfig := ensureStringMap(oauthConfig, "server")
	serverConfig["storage"] = map[string]interface{}{
		"type": StorageValkey,
		"valkey": map[string]interface{}{
			"url": v.addr(),
		},
	}
}

// ensureStringMap returns parent[key] as a string map, creating it when absent.
func ensureStringMap(parent map[string]interface{}, key string) map[string]interface{} {
	if existing, ok := toStringMap(parent[key]); ok {
		parent[key] = existing
		return existing
	}
	created := map[string]interface{}{}
	parent[key] = created
	return created
}
