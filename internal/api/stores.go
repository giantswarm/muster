package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// Capabilities holds the MCP capabilities for a session+server pair.
type Capabilities struct {
	Tools     []mcp.Tool
	Resources []mcp.Resource
	Prompts   []mcp.Prompt
}

// DeepCopy returns a shallow copy of each slice (elements are value types).
func (c *Capabilities) DeepCopy() *Capabilities {
	if c == nil {
		return nil
	}
	return &Capabilities{
		Tools:     append([]mcp.Tool(nil), c.Tools...),
		Resources: append([]mcp.Resource(nil), c.Resources...),
		Prompts:   append([]mcp.Prompt(nil), c.Prompts...),
	}
}

// DefaultCapabilityStoreTTL is the session-level TTL for capability entries.
const DefaultCapabilityStoreTTL = 30 * 24 * time.Hour

// SessionAuthStore tracks per-session, per-server authentication state.
// Implementations must be safe for concurrent use.
type SessionAuthStore interface {
	IsAuthenticated(ctx context.Context, sessionID, serverName string) (bool, error)
	MarkAuthenticated(ctx context.Context, sessionID, serverName string) error
	Revoke(ctx context.Context, sessionID, serverName string) error
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeServer(ctx context.Context, serverName string) error
	Touch(ctx context.Context, sessionID string) (bool, error)
}

// CapabilityStore stores per-session, per-server MCP capabilities.
// Implementations must be safe for concurrent use.
type CapabilityStore interface {
	Get(ctx context.Context, sessionID, serverName string) (*Capabilities, error)
	GetAll(ctx context.Context, sessionID string) (map[string]*Capabilities, error)
	Set(ctx context.Context, sessionID, serverName string, caps *Capabilities) error
	Delete(ctx context.Context, sessionID string) error
	DeleteEntry(ctx context.Context, sessionID, serverName string) error
	DeleteServer(ctx context.Context, serverName string) error
	Exists(ctx context.Context, sessionID, serverName string) (bool, error)
	Touch(ctx context.Context, sessionID string) (bool, error)
	ListSessions(ctx context.Context) ([]string, error)
}

// Store registry variables.
var (
	sessionAuthStoreInstance SessionAuthStore
	capabilityStoreInstance  CapabilityStore
	storeMutex               sync.RWMutex
)

// RegisterSessionAuthStore registers the session auth store implementation.
func RegisterSessionAuthStore(s SessionAuthStore) {
	storeMutex.Lock()
	defer storeMutex.Unlock()
	sessionAuthStoreInstance = s
}

// GetSessionAuthStore returns the registered session auth store, or an error if none is registered.
func GetSessionAuthStore() (SessionAuthStore, error) {
	storeMutex.RLock()
	defer storeMutex.RUnlock()
	if sessionAuthStoreInstance == nil {
		return nil, fmt.Errorf("session auth store not registered")
	}
	return sessionAuthStoreInstance, nil
}

// RegisterCapabilityStore registers the capability store implementation.
func RegisterCapabilityStore(s CapabilityStore) {
	storeMutex.Lock()
	defer storeMutex.Unlock()
	capabilityStoreInstance = s
}

// GetCapabilityStore returns the registered capability store, or an error if none is registered.
func GetCapabilityStore() (CapabilityStore, error) {
	storeMutex.RLock()
	defer storeMutex.RUnlock()
	if capabilityStoreInstance == nil {
		return nil, fmt.Errorf("capability store not registered")
	}
	return capabilityStoreInstance, nil
}
