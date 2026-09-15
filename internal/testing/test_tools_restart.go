package testing

import (
	"context"
	"fmt"
	"sort"

	"github.com/giantswarm/muster/internal/api"
)

// handleRestartInstance restarts the scenario's muster serve process while the
// Valkey stand-in and the mock servers keep running, then reconnects every
// client the scenario holds with the bearer it held. The same bearer is the
// same session to muster, so the steps after this one act as the sessions
// that lived through a rollout -- without a new sign-in.
func (h *TestToolsHandler) handleRestartInstance(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	pidBefore := 0
	if h.currentInstance.Process != nil {
		pidBefore = h.currentInstance.Process.Pid
	}

	if err := h.instanceManager.RestartInstance(ctx, h.currentInstance, h.logger); err != nil {
		return nil, fmt.Errorf("restart failed: %w", err)
	}

	reconnected, err := h.reconnectClients(ctx)
	if err != nil {
		return nil, err
	}

	pidAfter := 0
	if h.currentInstance.Process != nil {
		pidAfter = h.currentInstance.Process.Pid
	}
	return map[string]interface{}{
		api.FieldSuccess:    true,
		api.FieldMessage:    fmt.Sprintf("Restarted muster serve (PID %d -> %d); reconnected %d client(s)", pidBefore, pidAfter, len(reconnected)),
		"pid_before":        pidBefore,
		"pid_after":         pidAfter,
		"reconnected_users": reconnected,
	}, nil
}

// reconnectClients replaces every user client with a fresh connection to the
// restarted instance, presenting the bearer the old client presented. The
// current user's client stays the current one.
func (h *TestToolsHandler) reconnectClients(ctx context.Context) ([]string, error) {
	// The default user's live client is h.mcpClient while it is the current
	// user (test_muster_auth_login replaces it there); the map entry is what
	// as_user switches leave behind.
	if h.currentUser == defaultTestUser && h.mcpClient != nil {
		h.userClients[defaultTestUser] = h.mcpClient
	}

	names := make([]string, 0, len(h.userClients))
	for name := range h.userClients {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		old := h.userClients[name]
		token := ""
		if old != nil {
			token = old.AccessToken()
			_ = old.Close()
		}
		fresh := NewMCPTestClientWithLogger(h.debug, h.logger)
		var err error
		if token != "" {
			err = fresh.ConnectWithAuth(ctx, h.currentInstance.Endpoint, token)
		} else {
			err = fresh.Connect(ctx, h.currentInstance.Endpoint)
		}
		if err != nil {
			return nil, fmt.Errorf("reconnecting user %q after restart: %w", name, err)
		}
		h.userClients[name] = fresh
		if h.debug {
			h.logger.Debug("🔌 Reconnected user %s to the restarted instance\n", name)
		}
	}

	current := h.userClients[h.GetCurrentUserName()]
	if current == nil {
		current = h.userClients[defaultTestUser]
	}
	h.mcpClient = current
	return names, nil
}

// handleStopValkey takes the instance's Valkey stand-in off its port, data
// kept: the outage of a Valkey pod being rescheduled while muster keeps
// running.
func (h *TestToolsHandler) handleStopValkey(_ context.Context, _ map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	if err := h.instanceManager.StopValkey(h.currentInstance.ID); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		api.FieldSuccess: true,
		api.FieldMessage: "Stopped valkey; its data is kept for test_start_valkey",
	}, nil
}

// handleStartValkey brings the instance's Valkey stand-in back on its port
// with the data it had.
func (h *TestToolsHandler) handleStartValkey(_ context.Context, _ map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	if err := h.instanceManager.StartValkey(h.currentInstance.ID); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		api.FieldSuccess: true,
		api.FieldMessage: "Started valkey again on its port",
	}, nil
}
