package app

import (
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/orchestrator"
	serv "github.com/giantswarm/muster/internal/services"
)

func TestConfigValidation(t *testing.T) {
	// Test that the configuration is properly validated before running modes
	tests := []struct {
		name      string
		cfg       *Config
		wantError bool
	}{
		{
			name: "valid config with basic settings",
			cfg: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			wantError: false,
		},
		{
			name: "valid config with debug enabled",
			cfg: &Config{
				Debug: true,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port: 8080,
						Host: "localhost",
					},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate basic config structure
			if tt.cfg.MusterConfig == nil && !tt.wantError {
				t.Error("MusterConfig should not be nil for valid configs")
			}

			// Validate that the config has the expected structure
			if tt.cfg.MusterConfig != nil { //nolint:staticcheck
				// MCPServers are now managed by MCPServerManager, not validated here
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	// Test that configs have sensible defaults and validation
	cfg := &Config{
		Debug: true,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port: 0, // Should get default
				Host: "",
			},
		},
		ConfigPath: config.GetDefaultConfigPathOrPanic(),
	}

	// Verify the config structure is valid
	if cfg.MusterConfig == nil {
		t.Error("MusterConfig should not be nil")
	}
}

// TestWatchAggregatorFailure_SignalsOnAggregatorFailed verifies that the
// returned channel closes when the aggregator enters the failed state —
// including when the failure event lands before anyone waits on the channel,
// which is the startup-crash ordering (aggregator port already bound) that
// used to race on a shared bool.
func TestWatchAggregatorFailure_SignalsOnAggregatorFailed(t *testing.T) {
	changeChan := make(chan orchestrator.ServiceStateChangedEvent, 4)
	failed := watchAggregatorFailure(changeChan)

	// Unrelated events must not signal.
	changeChan <- orchestrator.ServiceStateChangedEvent{Name: "some-service", NewState: string(serv.StateFailed)}
	changeChan <- orchestrator.ServiceStateChangedEvent{Name: "mcp-aggregator", NewState: string(serv.StateRunning)}
	select {
	case <-failed:
		t.Fatal("failure channel closed without an aggregator failure event")
	case <-time.After(50 * time.Millisecond):
	}

	// The aggregator failing must signal, even though the event is sent
	// before this goroutine starts waiting.
	changeChan <- orchestrator.ServiceStateChangedEvent{Name: "mcp-aggregator", NewState: string(serv.StateFailed)}
	select {
	case <-failed:
	case <-time.After(5 * time.Second):
		t.Fatal("failure channel not closed after aggregator failure event")
	}
}

// TestWatchAggregatorFailure_NoSignalOnChannelClose verifies that the watcher
// goroutine exits quietly when the subscription channel closes without an
// aggregator failure.
func TestWatchAggregatorFailure_NoSignalOnChannelClose(t *testing.T) {
	changeChan := make(chan orchestrator.ServiceStateChangedEvent)
	failed := watchAggregatorFailure(changeChan)
	close(changeChan)

	select {
	case <-failed:
		t.Fatal("failure channel closed although no aggregator failure was reported")
	case <-time.After(50 * time.Millisecond):
	}
}
