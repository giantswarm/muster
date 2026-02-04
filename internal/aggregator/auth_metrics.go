package aggregator

import (
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// AuthMetrics tracks authentication-related metrics for monitoring and alerting.
//
// This provides visibility into authentication patterns, failures, and potential abuse.
// Metrics are tracked per-server to enable targeted alerting and debugging.
type AuthMetrics struct {
	mu sync.RWMutex

	// Per-server metrics
	loginAttempts  map[string]*authServerMetrics
	logoutAttempts map[string]*authServerMetrics

	// Global counters for summary metrics
	totalLoginAttempts   int64
	totalLoginSuccesses  int64
	totalLoginFailures   int64
	totalRateLimitBlocks int64
	totalLogoutAttempts  int64
	totalLogoutSuccesses int64
}

// authServerMetrics holds authentication metrics for a specific server.
type authServerMetrics struct {
	ServerName      string
	LoginAttempts   int64
	LoginSuccesses  int64
	LoginFailures   int64
	RateLimitBlocks int64
	LogoutAttempts  int64
	LogoutSuccesses int64
	LastAttemptAt   time.Time
	LastSuccessAt   time.Time
	LastFailureAt   time.Time
}

// NewAuthMetrics creates a new AuthMetrics instance.
func NewAuthMetrics() *AuthMetrics {
	return &AuthMetrics{
		loginAttempts:  make(map[string]*authServerMetrics),
		logoutAttempts: make(map[string]*authServerMetrics),
	}
}

// getOrCreateServerMetrics returns existing metrics for a server or creates new ones.
func (m *AuthMetrics) getOrCreateServerMetrics(serverName string) *authServerMetrics {
	if metrics, exists := m.loginAttempts[serverName]; exists {
		return metrics
	}

	metrics := &authServerMetrics{
		ServerName: serverName,
	}
	m.loginAttempts[serverName] = metrics
	return metrics
}

// RecordLoginAttempt records an authentication login attempt.
//
// Args:
//   - serverName: Name of the server being authenticated to
//   - sessionID: The session making the attempt (for logging, truncated)
func (m *AuthMetrics) RecordLoginAttempt(serverName, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateServerMetrics(serverName)
	metrics.LoginAttempts++
	metrics.LastAttemptAt = time.Now()
	m.totalLoginAttempts++

	logging.Debug("AuthMetrics", "Login attempt for server %s by session %s (total: %d)",
		serverName, logging.TruncateSessionID(sessionID), metrics.LoginAttempts)
}

// RecordLoginSuccess records a successful authentication.
//
// Args:
//   - serverName: Name of the server authenticated to
//   - sessionID: The session that authenticated (for logging, truncated)
func (m *AuthMetrics) RecordLoginSuccess(serverName, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateServerMetrics(serverName)
	metrics.LoginSuccesses++
	metrics.LastSuccessAt = time.Now()
	m.totalLoginSuccesses++

	logging.Info("AuthMetrics", "Login success for server %s by session %s (successes: %d, failures: %d)",
		serverName, logging.TruncateSessionID(sessionID), metrics.LoginSuccesses, metrics.LoginFailures)
}

// RecordLoginFailure records a failed authentication attempt.
//
// Args:
//   - serverName: Name of the server where authentication failed
//   - sessionID: The session that failed (for logging, truncated)
//   - reason: The reason for the failure
func (m *AuthMetrics) RecordLoginFailure(serverName, sessionID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateServerMetrics(serverName)
	metrics.LoginFailures++
	metrics.LastFailureAt = time.Now()
	m.totalLoginFailures++

	logging.Warn("AuthMetrics", "Login failure for server %s by session %s: %s (failures: %d)",
		serverName, logging.TruncateSessionID(sessionID), reason, metrics.LoginFailures)
}

// RecordRateLimitBlock records when a session was rate limited.
//
// Args:
//   - serverName: Name of the server the session was trying to authenticate to
//   - sessionID: The session that was blocked (for logging, truncated)
func (m *AuthMetrics) RecordRateLimitBlock(serverName, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateServerMetrics(serverName)
	metrics.RateLimitBlocks++
	m.totalRateLimitBlocks++

	logging.Warn("AuthMetrics", "Rate limit block for server %s by session %s (total blocks: %d)",
		serverName, logging.TruncateSessionID(sessionID), metrics.RateLimitBlocks)
}

// RecordLogoutAttempt records a logout attempt.
//
// Args:
//   - serverName: Name of the server being logged out from
//   - sessionID: The session making the attempt (for logging, truncated)
func (m *AuthMetrics) RecordLogoutAttempt(serverName, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateServerMetrics(serverName)
	metrics.LogoutAttempts++
	m.totalLogoutAttempts++

	logging.Debug("AuthMetrics", "Logout attempt for server %s by session %s",
		serverName, logging.TruncateSessionID(sessionID))
}

// RecordLogoutSuccess records a successful logout.
//
// Args:
//   - serverName: Name of the server logged out from
//   - sessionID: The session that logged out (for logging, truncated)
func (m *AuthMetrics) RecordLogoutSuccess(serverName, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateServerMetrics(serverName)
	metrics.LogoutSuccesses++
	m.totalLogoutSuccesses++

	logging.Info("AuthMetrics", "Logout success for server %s by session %s",
		serverName, logging.TruncateSessionID(sessionID))
}

// AuthMetricsSummary provides a summary of authentication metrics.
type AuthMetricsSummary struct {
	TotalLoginAttempts   int64                  `json:"total_login_attempts"`
	TotalLoginSuccesses  int64                  `json:"total_login_successes"`
	TotalLoginFailures   int64                  `json:"total_login_failures"`
	TotalRateLimitBlocks int64                  `json:"total_rate_limit_blocks"`
	TotalLogoutAttempts  int64                  `json:"total_logout_attempts"`
	TotalLogoutSuccesses int64                  `json:"total_logout_successes"`
	PerServerMetrics     []AuthServerMetricView `json:"per_server_metrics"`
}

// AuthServerMetricView is a read-only view of server-specific auth metrics.
type AuthServerMetricView struct {
	ServerName      string    `json:"server_name"`
	LoginAttempts   int64     `json:"login_attempts"`
	LoginSuccesses  int64     `json:"login_successes"`
	LoginFailures   int64     `json:"login_failures"`
	RateLimitBlocks int64     `json:"rate_limit_blocks"`
	LogoutAttempts  int64     `json:"logout_attempts"`
	LogoutSuccesses int64     `json:"logout_successes"`
	LastAttemptAt   time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt   time.Time `json:"last_success_at,omitempty"`
	LastFailureAt   time.Time `json:"last_failure_at,omitempty"`
}

// GetSummary returns a summary of all authentication metrics.
func (m *AuthMetrics) GetSummary() AuthMetricsSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summary := AuthMetricsSummary{
		TotalLoginAttempts:   m.totalLoginAttempts,
		TotalLoginSuccesses:  m.totalLoginSuccesses,
		TotalLoginFailures:   m.totalLoginFailures,
		TotalRateLimitBlocks: m.totalRateLimitBlocks,
		TotalLogoutAttempts:  m.totalLogoutAttempts,
		TotalLogoutSuccesses: m.totalLogoutSuccesses,
	}

	for _, metrics := range m.loginAttempts {
		summary.PerServerMetrics = append(summary.PerServerMetrics, AuthServerMetricView{
			ServerName:      metrics.ServerName,
			LoginAttempts:   metrics.LoginAttempts,
			LoginSuccesses:  metrics.LoginSuccesses,
			LoginFailures:   metrics.LoginFailures,
			RateLimitBlocks: metrics.RateLimitBlocks,
			LogoutAttempts:  metrics.LogoutAttempts,
			LogoutSuccesses: metrics.LogoutSuccesses,
			LastAttemptAt:   metrics.LastAttemptAt,
			LastSuccessAt:   metrics.LastSuccessAt,
			LastFailureAt:   metrics.LastFailureAt,
		})
	}

	return summary
}

// GetServerMetrics returns metrics for a specific server.
func (m *AuthMetrics) GetServerMetrics(serverName string) (AuthServerMetricView, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metrics, exists := m.loginAttempts[serverName]
	if !exists {
		return AuthServerMetricView{}, false
	}

	return AuthServerMetricView{
		ServerName:      metrics.ServerName,
		LoginAttempts:   metrics.LoginAttempts,
		LoginSuccesses:  metrics.LoginSuccesses,
		LoginFailures:   metrics.LoginFailures,
		RateLimitBlocks: metrics.RateLimitBlocks,
		LogoutAttempts:  metrics.LogoutAttempts,
		LogoutSuccesses: metrics.LogoutSuccesses,
		LastAttemptAt:   metrics.LastAttemptAt,
		LastSuccessAt:   metrics.LastSuccessAt,
		LastFailureAt:   metrics.LastFailureAt,
	}, true
}
