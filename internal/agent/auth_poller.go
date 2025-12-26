package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// AuthPollInterval is the interval for polling auth status from the server.
const AuthPollInterval = 30 * time.Second

// AuthStatusResourceURI is the URI for the auth status MCP resource.
const AuthStatusResourceURI = "auth://status"

// AuthRequiredInfo contains information about a server requiring authentication.
// This is used to build human-readable notifications and structured _meta data.
type AuthRequiredInfo struct {
	Server   string `json:"server"`
	Issuer   string `json:"issuer"`
	Scope    string `json:"scope,omitempty"`
	AuthTool string `json:"auth_tool"`
}

// AuthStatusResponse mirrors the aggregator's response from auth://status.
type AuthStatusResponse struct {
	Servers []ServerAuthStatus `json:"servers"`
}

// ServerAuthStatus represents the auth status of a single server.
type ServerAuthStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Issuer   string `json:"issuer,omitempty"`
	Scope    string `json:"scope,omitempty"`
	AuthTool string `json:"auth_tool,omitempty"`
	Error    string `json:"error,omitempty"`
}

// authPoller handles polling for auth status and caching the results.
type authPoller struct {
	client       *Client
	logger       *Logger
	cache        []AuthRequiredInfo
	mu           sync.RWMutex
	stopCh       chan struct{}
	pollInterval time.Duration
}

// newAuthPoller creates a new auth poller.
func newAuthPoller(client *Client, logger *Logger) *authPoller {
	return &authPoller{
		client:       client,
		logger:       logger,
		cache:        []AuthRequiredInfo{},
		stopCh:       make(chan struct{}),
		pollInterval: AuthPollInterval,
	}
}

// Start begins polling for auth status.
func (p *authPoller) Start(ctx context.Context) {
	// Do an initial poll immediately
	p.pollAuthStatus(ctx)

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.pollAuthStatus(ctx)
		}
	}
}

// Stop stops the auth poller.
func (p *authPoller) Stop() {
	close(p.stopCh)
}

// pollAuthStatus fetches the auth://status resource and updates the cache.
func (p *authPoller) pollAuthStatus(ctx context.Context) {
	resource, err := p.client.GetResource(ctx, AuthStatusResourceURI)
	if err != nil {
		// Silently continue with cached data - auth status is best-effort
		return
	}

	// Parse the response content
	if len(resource.Contents) == 0 {
		return
	}

	var responseText string
	for _, content := range resource.Contents {
		if textContent, ok := mcp.AsTextResourceContents(content); ok {
			responseText = textContent.Text
			break
		}
	}

	if responseText == "" {
		return
	}

	var status AuthStatusResponse
	if err := json.Unmarshal([]byte(responseText), &status); err != nil {
		return
	}

	// Build the auth required list
	var authRequired []AuthRequiredInfo
	for _, srv := range status.Servers {
		if srv.Status == "auth_required" {
			authRequired = append(authRequired, AuthRequiredInfo{
				Server:   srv.Name,
				Issuer:   srv.Issuer,
				Scope:    srv.Scope,
				AuthTool: srv.AuthTool,
			})
		}
	}

	p.mu.Lock()
	p.cache = authRequired
	p.mu.Unlock()
}

// GetAuthRequired returns the cached list of servers requiring authentication.
func (p *authPoller) GetAuthRequired() []AuthRequiredInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]AuthRequiredInfo, len(p.cache))
	copy(result, p.cache)
	return result
}

// HasAuthRequired returns true if any servers require authentication.
func (p *authPoller) HasAuthRequired() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.cache) > 0
}
