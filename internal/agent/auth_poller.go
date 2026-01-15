package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	pkgoauth "muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// AuthPollInterval is the interval for polling auth status from the server.
const AuthPollInterval = 30 * time.Second

// AuthStatusResourceURI is the URI for the auth status MCP resource.
const AuthStatusResourceURI = "auth://status"

// authPoller handles polling for auth status and caching the results.
type authPoller struct {
	client       *Client
	logger       *Logger
	cache        []pkgoauth.AuthRequiredInfo
	mu           sync.RWMutex
	stopCh       chan struct{}
	pollInterval time.Duration
}

// newAuthPoller creates a new auth poller.
func newAuthPoller(client *Client, logger *Logger) *authPoller {
	return &authPoller{
		client:       client,
		logger:       logger,
		cache:        []pkgoauth.AuthRequiredInfo{},
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

	var status pkgoauth.AuthStatusResponse
	if err := json.Unmarshal([]byte(responseText), &status); err != nil {
		return
	}

	// Build the auth required list
	var authRequired []pkgoauth.AuthRequiredInfo
	for _, srv := range status.Servers {
		if srv.Status == "auth_required" {
			authRequired = append(authRequired, pkgoauth.AuthRequiredInfo{
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
func (p *authPoller) GetAuthRequired() []pkgoauth.AuthRequiredInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]pkgoauth.AuthRequiredInfo, len(p.cache))
	copy(result, p.cache)
	return result
}

// HasAuthRequired returns true if any servers require authentication.
func (p *authPoller) HasAuthRequired() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.cache) > 0
}
