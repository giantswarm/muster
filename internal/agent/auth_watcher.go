package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"muster/internal/agent/oauth"
	"muster/pkg/auth"
)

// DefaultAuthWatcherPollInterval is the default interval for polling auth status.
const DefaultAuthWatcherPollInterval = 10 * time.Second

// Exponential backoff constants for handling poll failures.
const (
	// minBackoffInterval is the minimum backoff interval after a failure.
	minBackoffInterval = 1 * time.Second

	// maxBackoffInterval is the maximum backoff interval after repeated failures.
	maxBackoffInterval = 5 * time.Minute

	// backoffMultiplier is the factor by which the backoff interval increases.
	backoffMultiplier = 2
)

// AuthWatcherCallbacks contains callback functions for auth events.
type AuthWatcherCallbacks struct {
	// OnBrowserAuthRequired is called when browser authentication is needed.
	// It receives the server name and the auth tool name to call.
	OnBrowserAuthRequired func(serverName, authToolName string)

	// OnAuthComplete is called when authentication completes for a server.
	OnAuthComplete func(serverName string)

	// OnAuthError is called when an authentication error occurs.
	OnAuthError func(serverName string, err error)

	// OnTokenSubmitted is called when a token is successfully submitted via SSO.
	OnTokenSubmitted func(serverName, issuer string)
}

// AuthWatcher watches for authentication state changes and handles SSO.
// It continuously polls the auth://status resource and forwards tokens
// when matching issuers are found in the token store.
//
// The watcher implements exponential backoff on consecutive failures to prevent
// overwhelming the server during connectivity issues or server problems.
type AuthWatcher struct {
	mu           sync.RWMutex
	client       *Client
	tokenStore   *oauth.TokenStore
	pollInterval time.Duration
	logger       *slog.Logger
	callbacks    AuthWatcherCallbacks
	lastStatus   *auth.StatusResponse
	running      bool
	stopCh       chan struct{}

	// Backoff state for handling consecutive failures
	consecutiveFailures int
	currentBackoff      time.Duration
}

// AuthWatcherOption configures the AuthWatcher.
type AuthWatcherOption func(*AuthWatcher)

// WithPollInterval sets the poll interval for the AuthWatcher.
func WithPollInterval(interval time.Duration) AuthWatcherOption {
	return func(w *AuthWatcher) {
		w.pollInterval = interval
	}
}

// WithLogger sets the logger for the AuthWatcher.
func WithLogger(logger *slog.Logger) AuthWatcherOption {
	return func(w *AuthWatcher) {
		w.logger = logger
	}
}

// WithCallbacks sets the callbacks for the AuthWatcher.
func WithCallbacks(callbacks AuthWatcherCallbacks) AuthWatcherOption {
	return func(w *AuthWatcher) {
		w.callbacks = callbacks
	}
}

// NewAuthWatcher creates a new AuthWatcher.
func NewAuthWatcher(client *Client, tokenStore *oauth.TokenStore, opts ...AuthWatcherOption) *AuthWatcher {
	w := &AuthWatcher{
		client:       client,
		tokenStore:   tokenStore,
		pollInterval: DefaultAuthWatcherPollInterval,
		logger:       slog.Default(),
		stopCh:       make(chan struct{}),
	}

	for _, opt := range opts {
		opt(w)
	}

	return w
}

// Start begins watching for authentication state changes.
// This method blocks until the context is cancelled or Stop is called.
// The watcher uses exponential backoff when encountering repeated failures
// to prevent overwhelming the server during connectivity issues.
func (w *AuthWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.consecutiveFailures = 0
	w.currentBackoff = 0
	w.mu.Unlock()

	// Do an initial check immediately
	w.checkAuthStatus(ctx)

	for {
		// Use dynamic interval that accounts for backoff
		interval := w.getEffectivePollInterval()
		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			timer.Stop()
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			return

		case <-w.stopCh:
			timer.Stop()
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			return

		case <-timer.C:
			w.checkAuthStatus(ctx)
		}
	}
}

// Stop stops the auth watcher.
func (w *AuthWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		close(w.stopCh)
	}
}

// IsRunning returns whether the auth watcher is currently running.
func (w *AuthWatcher) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// checkAuthStatus fetches the current auth status and handles any changes.
// Implements exponential backoff on consecutive failures.
func (w *AuthWatcher) checkAuthStatus(ctx context.Context) {
	status, err := w.fetchAuthStatus(ctx)
	if err != nil {
		w.handleFetchFailure(err)
		return
	}

	// Reset backoff on success
	w.resetBackoff()

	// Detect new challenges and resolved challenges
	newChallenges := w.detectNewChallenges(w.lastStatus, status)
	resolvedChallenges := w.detectResolvedChallenges(w.lastStatus, status)

	// Handle new challenges
	for _, challenge := range newChallenges {
		w.handleNewChallenge(ctx, challenge)
	}

	// Handle resolved challenges
	for _, serverName := range resolvedChallenges {
		if w.callbacks.OnAuthComplete != nil {
			w.callbacks.OnAuthComplete(serverName)
		}
	}

	// Update last status
	w.mu.Lock()
	w.lastStatus = status
	w.mu.Unlock()
}

// handleFetchFailure handles a failure to fetch auth status with exponential backoff.
func (w *AuthWatcher) handleFetchFailure(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.consecutiveFailures++

	// Calculate backoff interval with exponential increase
	if w.currentBackoff == 0 {
		w.currentBackoff = minBackoffInterval
	} else {
		w.currentBackoff *= backoffMultiplier
		if w.currentBackoff > maxBackoffInterval {
			w.currentBackoff = maxBackoffInterval
		}
	}

	// Log with appropriate level based on failure count
	if w.consecutiveFailures <= 3 {
		w.logger.Debug("Failed to fetch auth status",
			"error", err,
			"consecutive_failures", w.consecutiveFailures,
			"backoff", w.currentBackoff,
		)
	} else {
		w.logger.Warn("Repeated auth status fetch failures",
			"error", err,
			"consecutive_failures", w.consecutiveFailures,
			"backoff", w.currentBackoff,
		)
	}
}

// resetBackoff resets the backoff state after a successful fetch.
func (w *AuthWatcher) resetBackoff() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.consecutiveFailures > 0 {
		w.logger.Debug("Auth status fetch recovered",
			"previous_failures", w.consecutiveFailures,
		)
	}

	w.consecutiveFailures = 0
	w.currentBackoff = 0
}

// getEffectivePollInterval returns the current poll interval, accounting for backoff.
func (w *AuthWatcher) getEffectivePollInterval() time.Duration {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.currentBackoff > 0 {
		return w.currentBackoff
	}
	return w.pollInterval
}

// fetchAuthStatus retrieves the auth://status resource.
func (w *AuthWatcher) fetchAuthStatus(ctx context.Context) (*auth.StatusResponse, error) {
	resource, err := w.client.GetResource(ctx, "auth://status")
	if err != nil {
		return nil, err
	}

	if len(resource.Contents) == 0 {
		return nil, nil
	}

	// Extract text content
	var jsonData string
	for _, content := range resource.Contents {
		if textContent, ok := content.(interface{ GetText() string }); ok {
			jsonData = textContent.GetText()
			break
		}
	}

	if jsonData == "" {
		return nil, nil
	}

	var status auth.StatusResponse
	if err := json.Unmarshal([]byte(jsonData), &status); err != nil {
		return nil, err
	}

	return &status, nil
}

// detectNewChallenges finds servers that newly require authentication.
func (w *AuthWatcher) detectNewChallenges(oldStatus, newStatus *auth.StatusResponse) []auth.ServerAuthStatus {
	if newStatus == nil {
		return nil
	}

	var newChallenges []auth.ServerAuthStatus

	// Build map of old auth_required servers
	oldAuthRequired := make(map[string]bool)
	if oldStatus != nil {
		for _, s := range oldStatus.ServerAuths {
			if s.Status == "auth_required" {
				oldAuthRequired[s.ServerName] = true
			}
		}
	}

	// Find new auth_required servers
	for _, s := range newStatus.ServerAuths {
		if s.Status == "auth_required" && !oldAuthRequired[s.ServerName] {
			newChallenges = append(newChallenges, s)
		}
	}

	return newChallenges
}

// detectResolvedChallenges finds servers that were auth_required but are now connected.
func (w *AuthWatcher) detectResolvedChallenges(oldStatus, newStatus *auth.StatusResponse) []string {
	if oldStatus == nil || newStatus == nil {
		return nil
	}

	var resolved []string

	// Build map of old auth_required servers
	oldAuthRequired := make(map[string]bool)
	for _, s := range oldStatus.ServerAuths {
		if s.Status == "auth_required" {
			oldAuthRequired[s.ServerName] = true
		}
	}

	// Find servers that changed from auth_required to connected
	for _, s := range newStatus.ServerAuths {
		if s.Status == "connected" && oldAuthRequired[s.ServerName] {
			resolved = append(resolved, s.ServerName)
		}
	}

	return resolved
}

// handleNewChallenge handles a new authentication challenge.
// SECURITY: Logs SSO operations for audit trail without logging token values.
func (w *AuthWatcher) handleNewChallenge(ctx context.Context, challenge auth.ServerAuthStatus) {
	if challenge.AuthChallenge == nil {
		w.logger.Debug("No auth challenge info for server", "server", challenge.ServerName)
		return
	}

	issuer := challenge.AuthChallenge.Issuer
	if issuer == "" {
		w.logger.Debug("No issuer in auth challenge", "server", challenge.ServerName)
		return
	}

	// Check if we have a token for this issuer (SSO)
	token := w.tokenStore.GetByIssuer(issuer)
	if token != nil {
		// SECURITY AUDIT: SSO token found, attempting to forward
		w.logger.Info("SECURITY_AUDIT: SSO token lookup successful",
			"event", "sso_token_found",
			"server", challenge.ServerName,
			"issuer", issuer,
			"token_server_url", token.ServerURL,
		)

		// Submit the token to the server
		// SECURITY: Token value is passed but never logged
		if err := w.submitToken(ctx, challenge.ServerName, token.AccessToken); err != nil {
			// SECURITY AUDIT: SSO token submission failed
			w.logger.Warn("SECURITY_AUDIT: SSO token submission failed",
				"event", "sso_submit_failed",
				"server", challenge.ServerName,
				"issuer", issuer,
				"error", err,
			)
			// Fall through to browser auth
		} else {
			// SECURITY AUDIT: SSO token submission successful
			w.logger.Info("SECURITY_AUDIT: SSO authentication successful",
				"event", "sso_auth_success",
				"server", challenge.ServerName,
				"issuer", issuer,
			)
			if w.callbacks.OnTokenSubmitted != nil {
				w.callbacks.OnTokenSubmitted(challenge.ServerName, issuer)
			}
			return // Token submitted successfully
		}
	}

	// Need browser authentication
	// SECURITY AUDIT: No SSO token available, browser auth required
	w.logger.Info("SECURITY_AUDIT: Browser authentication required",
		"event", "browser_auth_required",
		"server", challenge.ServerName,
		"issuer", issuer,
	)

	if w.callbacks.OnBrowserAuthRequired != nil {
		w.callbacks.OnBrowserAuthRequired(challenge.ServerName, challenge.AuthChallenge.AuthToolName)
	}
}

// submitToken submits an access token to the aggregator for a specific server.
func (w *AuthWatcher) submitToken(ctx context.Context, serverName, accessToken string) error {
	args := map[string]interface{}{
		"server_name":  serverName,
		"access_token": accessToken,
	}

	result, err := w.client.CallTool(ctx, "submit_auth_token", args)
	if err != nil {
		return err
	}

	// Check for tool-level errors
	if result != nil && result.IsError {
		return w.extractToolError(result)
	}

	return nil
}

// extractToolError extracts an error message from a tool result.
func (w *AuthWatcher) extractToolError(result *mcp.CallToolResult) error {
	if result == nil || len(result.Content) == 0 {
		return fmt.Errorf("tool returned an error")
	}

	// Extract text from the first content item
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			return fmt.Errorf("tool error: %s", textContent.Text)
		}
	}

	return fmt.Errorf("tool returned an error")
}

// GetLastStatus returns the last fetched auth status.
func (w *AuthWatcher) GetLastStatus() *auth.StatusResponse {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastStatus
}
