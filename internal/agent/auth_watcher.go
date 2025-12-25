package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"muster/internal/agent/oauth"
)

// DefaultAuthWatcherPollInterval is the default interval for polling auth status.
const DefaultAuthWatcherPollInterval = 10 * time.Second

// AuthStatusResponse represents the structured authentication state
// returned by the auth://status resource.
type AuthStatusResponse struct {
	MusterAuth  *MusterAuthStatus  `json:"muster_auth"`
	ServerAuths []ServerAuthStatus `json:"server_auths"`
}

// MusterAuthStatus describes the authentication state for Muster Server.
type MusterAuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	Issuer        string `json:"issuer,omitempty"`
}

// ServerAuthStatus describes the authentication state for a remote MCP server.
type ServerAuthStatus struct {
	ServerName    string             `json:"server_name"`
	Status        string             `json:"status"`
	AuthChallenge *AuthChallengeInfo `json:"auth_challenge,omitempty"`
	Error         string             `json:"error,omitempty"`
}

// AuthChallengeInfo contains information about an authentication challenge.
type AuthChallengeInfo struct {
	Issuer       string `json:"issuer"`
	Scope        string `json:"scope,omitempty"`
	AuthToolName string `json:"auth_tool_name"`
}

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
type AuthWatcher struct {
	mu           sync.RWMutex
	client       *Client
	tokenStore   *oauth.TokenStore
	pollInterval time.Duration
	logger       *slog.Logger
	callbacks    AuthWatcherCallbacks
	lastStatus   *AuthStatusResponse
	running      bool
	stopCh       chan struct{}
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
func (w *AuthWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Do an initial check immediately
	w.checkAuthStatus(ctx)

	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			return

		case <-w.stopCh:
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			return

		case <-ticker.C:
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
func (w *AuthWatcher) checkAuthStatus(ctx context.Context) {
	status, err := w.fetchAuthStatus(ctx)
	if err != nil {
		w.logger.Debug("Failed to fetch auth status", "error", err)
		return
	}

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

// fetchAuthStatus retrieves the auth://status resource.
func (w *AuthWatcher) fetchAuthStatus(ctx context.Context) (*AuthStatusResponse, error) {
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

	var status AuthStatusResponse
	if err := json.Unmarshal([]byte(jsonData), &status); err != nil {
		return nil, err
	}

	return &status, nil
}

// detectNewChallenges finds servers that newly require authentication.
func (w *AuthWatcher) detectNewChallenges(oldStatus, newStatus *AuthStatusResponse) []ServerAuthStatus {
	if newStatus == nil {
		return nil
	}

	var newChallenges []ServerAuthStatus

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
func (w *AuthWatcher) detectResolvedChallenges(oldStatus, newStatus *AuthStatusResponse) []string {
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
func (w *AuthWatcher) handleNewChallenge(ctx context.Context, challenge ServerAuthStatus) {
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
		w.logger.Info("SSO: Found existing token for issuer",
			"server", challenge.ServerName,
			"issuer", issuer,
		)

		// Submit the token to the server
		if err := w.submitToken(ctx, challenge.ServerName, token.AccessToken); err != nil {
			w.logger.Warn("Failed to submit token via SSO",
				"server", challenge.ServerName,
				"issuer", issuer,
				"error", err,
			)
			// Fall through to browser auth
		} else {
			if w.callbacks.OnTokenSubmitted != nil {
				w.callbacks.OnTokenSubmitted(challenge.ServerName, issuer)
			}
			return // Token submitted successfully
		}
	}

	// Need browser authentication
	w.logger.Info("Browser authentication required",
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
func (w *AuthWatcher) extractToolError(result interface{}) error {
	// This is a simplified error extraction - the actual implementation
	// would depend on the tool result structure
	return nil
}

// GetLastStatus returns the last fetched auth status.
func (w *AuthWatcher) GetLastStatus() *AuthStatusResponse {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastStatus
}
