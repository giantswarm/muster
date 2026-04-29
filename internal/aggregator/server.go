package aggregator

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/admin"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	internalmcp "github.com/giantswarm/muster/internal/mcpserver"
	musteroauth "github.com/giantswarm/muster/internal/oauth"
	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/internal/teleport"
	v1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/coreos/go-systemd/v22/activation"
	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/valkey-io/valkey-go"
	"golang.org/x/oauth2"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// AggregatorServer implements a comprehensive MCP server that aggregates multiple backend MCP servers.
//
// The AggregatorServer is the core component responsible for:
//   - Collecting and exposing tools, resources, and prompts from multiple backend servers
//   - Managing multiple transport protocols (SSE, stdio, streamable-http)
//   - Integrating core muster tools alongside external MCP servers
//   - Providing intelligent name collision resolution
//   - Implementing security filtering through the denylist system
//   - Real-time capability updates when backend servers change
//   - User-scoped tool visibility for OAuth-protected servers
//
// Architecture:
// The server maintains a registry of backend MCP servers and dynamically updates its
// exposed capabilities as servers are registered/deregistered. It supports multiple
// transport protocols simultaneously and provides both external MCP compatibility
// and internal tool calling capabilities.
//
// Session-Scoped Tool Visibility:
// For OAuth-protected servers, each login session's tool view is determined by the
// CapabilityStore keyed by (sessionID, serverName). There is no session registry;
// connections are created on demand for each tool call.
//
// Thread Safety:
// All public methods are thread-safe and can be called concurrently. Internal state
// is protected by appropriate synchronization mechanisms.
type AggregatorServer struct {
	config    AggregatorConfig     // Configuration args for the aggregator
	registry  *ServerRegistry      // Registry of backend MCP servers
	mcpServer *mcpserver.MCPServer // Core MCP server implementation

	errorCallback func(error) // Callback for propagating async errors in the aggregator upwards

	// Transport-specific server instances for different communication protocols
	sseServer            *mcpserver.SSEServer            // Server-Sent Events transport
	streamableHTTPServer *mcpserver.StreamableHTTPServer // Streamable HTTP transport
	stdioServer          *mcpserver.StdioServer          // Standard I/O transport

	// HTTP servers with socket options (when socket reuse is enabled)
	httpServer []*http.Server

	// OAuth HTTP server for protecting MCP endpoints (when OAuth server is enabled)
	oauthHTTPServer *server.OAuthHTTPServer

	// Lifecycle management for coordinating startup and shutdown
	ctx        context.Context    // Context for coordinating shutdown
	cancelFunc context.CancelFunc // Function to cancel the context
	wg         sync.WaitGroup     // WaitGroup for background goroutines
	mu         sync.RWMutex       // Protects server state during lifecycle operations

	// Active capability tracking - manages which meta-tools are currently exposed
	toolManager    *activeItemManager // Tracks active meta-tools
	isShuttingDown bool               // Indicates whether the server is currently stopping

	// Authentication rate limiting and metrics (security hardening per ADR-008)
	authRateLimiter *AuthRateLimiter // Per-user rate limiting for auth operations
	authMetrics     *AuthMetrics     // Authentication metrics for monitoring

	// Per-session auth store tracks which sessions have authenticated to which servers.
	// Separated from capabilityStore so that clearing stale capabilities does not
	// accidentally revoke authentication (see capability freshness plan).
	// Always non-nil after NewAggregatorServer; nil checks in methods exist only
	// for test code that constructs partial AggregatorServer instances.
	authStore SessionAuthStore

	// Per-session capability store for OAuth servers (on-demand population).
	// Always non-nil after NewAggregatorServer.
	capabilityStore CapabilityStore

	// Per-session connection pool for reusing live MCP clients across tool calls.
	// Always non-nil after NewAggregatorServer.
	connPool *SessionConnectionPool

	// tokenRefreshGroup deduplicates concurrent background token refresh
	// goroutines for the same (sessionID, serverName) key. This prevents
	// multiple tool calls from spawning parallel token exchanges.
	tokenRefreshGroup singleflight.Group

	// notifRefreshGroup deduplicates concurrent capability re-fetches
	// triggered by server-pushed notifications/tools/list_changed.
	notifRefreshGroup singleflight.Group

	// SSO tracking for proactive SSO initialization (replaces SessionRegistry SSO methods)
	ssoTracker *ssoTracker

	// Maps user subjects to their MCP client session IDs for targeted notifications.
	// Populated in sessionToolFilter, cleaned up via OnUnregisterSession hook.
	subjectSessions *subjectSessionTracker

	// valkeyClient is the shared Valkey client used by authStore and capabilityStore
	// when Valkey storage is configured. Nil when using in-memory stores.
	// Closed during Stop().
	valkeyClient valkey.Client

	// valkeyKeyPrefix is the key prefix used by all Valkey stores (default "muster:").
	valkeyKeyPrefix string

	// valkeyEncryptor provides AES-256-GCM encryption at rest for sensitive values
	// stored in Valkey (tokens, state). Nil when encryption is not configured.
	valkeyEncryptor *security.Encryptor

	// adminServer is the optional admin web UI listener. Nil when disabled.
	adminServer *admin.Server

	// transportDispatcher resolves CR-driven outbound HTTP clients (TB-7/TB-8).
	// nil when no Teleport clusters are configured — direct-HTTPS is then used
	// unconditionally, preserving customer-Muster behavior.
	transportDispatcher teleport.TransportDispatcher

	// k8sClient is the controller-runtime client used to patch
	// MCPServer.status.conditions[type=TransportReady] when the dispatcher
	// reports a structured error (TB-8). nil disables condition updates.
	k8sClient ctrlclient.Client
}

// getValkeyClient returns the shared Valkey client if one was configured,
// or nil when using in-memory stores. Used by AggregatorManager to share the
// client with the OAuth token/state stores.
func (a *AggregatorServer) getValkeyClient() valkey.Client {
	return a.valkeyClient
}

// getValkeyKeyPrefix returns the configured key prefix for Valkey stores.
func (a *AggregatorServer) getValkeyKeyPrefix() string {
	return a.valkeyKeyPrefix
}

// getValkeyEncryptor returns the AES-256-GCM encryptor for Valkey stores,
// or nil if encryption is not configured.
func (a *AggregatorServer) getValkeyEncryptor() *security.Encryptor {
	return a.valkeyEncryptor
}

// subjectSessionTracker maps user subjects to their MCP client session IDs
// (for targeted notifications) and to their OAuth session IDs / token family
// IDs (for the admin UI). The two ID spaces are intentionally kept separate
// because polluting the MCP-session map with OAuth IDs would break
// notification delivery.
//
// A reverse map keyed by MCP session ID allows O(1) cleanup when the
// transport session is unregistered.
type subjectSessionTracker struct {
	mu                      sync.RWMutex
	sessions                map[string]map[string]bool // sub -> set of MCP session IDs
	reverse                 map[string]string          // MCP session ID -> sub
	subjectByOAuthSessionID map[string]string          // OAuth session ID -> sub
}

func newSubjectSessionTracker() *subjectSessionTracker {
	return &subjectSessionTracker{
		sessions:                make(map[string]map[string]bool),
		reverse:                 make(map[string]string),
		subjectByOAuthSessionID: make(map[string]string),
	}
}

// TrackOAuth records a mapping from subject to OAuth session ID (token family).
// Called from sessionToolFilter and BeforeCallTool so the admin UI can render a
// subject for every live OAuth session, even ones that haven't reached the
// cap store yet.
func (t *subjectSessionTracker) TrackOAuth(sub, oauthSessionID string) {
	if sub == "" || oauthSessionID == "" {
		return
	}
	// Fast-path: if already recorded, avoid the write-lock. OAuth session IDs
	// are stable for the life of a token family, so concurrent tool calls
	// only need to write once.
	t.mu.RLock()
	if t.subjectByOAuthSessionID[oauthSessionID] == sub {
		t.mu.RUnlock()
		return
	}
	t.mu.RUnlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.subjectByOAuthSessionID[oauthSessionID] = sub
}

// UntrackOAuth removes an OAuth session ID from the tracker. Called during
// session teardown so the map doesn't grow unbounded.
func (t *subjectSessionTracker) UntrackOAuth(oauthSessionID string) {
	if oauthSessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subjectByOAuthSessionID, oauthSessionID)
}

// OAuthSubject returns the subject associated with an OAuth session ID, or
// "" if none is tracked.
func (t *subjectSessionTracker) OAuthSubject(oauthSessionID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.subjectByOAuthSessionID[oauthSessionID]
}

// OAuthSessionIDs returns the set of tracked OAuth session IDs. Intended for
// admin enumeration paths.
func (t *subjectSessionTracker) OAuthSessionIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.subjectByOAuthSessionID))
	for id := range t.subjectByOAuthSessionID {
		out = append(out, id)
	}
	return out
}

// Track records a mapping from user subject to MCP session ID.
func (t *subjectSessionTracker) Track(sub, mcpSessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessions[sub] == nil {
		t.sessions[sub] = make(map[string]bool)
	}
	t.sessions[sub][mcpSessionID] = true
	t.reverse[mcpSessionID] = sub
}

// RemoveSession removes an MCP session ID using the reverse map for O(1) lookup.
func (t *subjectSessionTracker) RemoveSession(mcpSessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sub, ok := t.reverse[mcpSessionID]
	if !ok {
		return
	}
	delete(t.reverse, mcpSessionID)
	if ids, exists := t.sessions[sub]; exists {
		delete(ids, mcpSessionID)
		if len(ids) == 0 {
			delete(t.sessions, sub)
		}
	}
}

// GetSessionIDs returns the MCP session IDs for the given user subject.
func (t *subjectSessionTracker) GetSessionIDs(sub string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ids := t.sessions[sub]
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}

// ssoFailedEntry records when an SSO attempt failed, enabling TTL-based expiry
// with exponential backoff for repeated failures on the same server.
type ssoFailedEntry struct {
	failedAt     time.Time
	failureCount int
}

// ssoTrackerFailureTTL is the base duration after which a first SSO failure
// entry expires. Subsequent failures for the same user/server pair use
// exponential backoff (2x per failure) up to ssoBackoffMaxTTL.
const ssoTrackerFailureTTL = 5 * time.Minute

// ssoBackoffMaxTTL caps the exponential backoff for repeated SSO failures.
// After this many consecutive failures the retry interval stays constant.
const ssoBackoffMaxTTL = 30 * time.Minute

// ssoTrackerPendingTimeout is how long sso_pending is reported before falling
// back to auth_required. This prevents the client from being stuck in a pending
// state when an SSO attempt silently fails without being recorded.
const ssoTrackerPendingTimeout = 30 * time.Second

// ssoBackoffDuration returns the backoff duration for the given failure count.
// The first failure uses ssoTrackerFailureTTL; each subsequent failure doubles
// the wait, capped at ssoBackoffMaxTTL.
func ssoBackoffDuration(failureCount int) time.Duration {
	if failureCount <= 1 {
		return ssoTrackerFailureTTL
	}
	shift := failureCount - 1
	if shift > 6 {
		return ssoBackoffMaxTTL
	}
	d := ssoTrackerFailureTTL * (1 << shift)
	if d > ssoBackoffMaxTTL {
		return ssoBackoffMaxTTL
	}
	return d
}

// ssoTracker tracks SSO pending and failure state per user subject and server.
// Used by on-demand SSO to record and check SSO connection status.
type ssoTracker struct {
	mu             sync.RWMutex
	pendingServers map[string]map[string]time.Time       // sub -> serverName -> firstSeen
	failedServers  map[string]map[string]*ssoFailedEntry // sub -> serverName -> entry
}

func newSSOTracker() *ssoTracker {
	return &ssoTracker{
		pendingServers: make(map[string]map[string]time.Time),
		failedServers:  make(map[string]map[string]*ssoFailedEntry),
	}
}

// MarkSSOPending records that an SSO connection attempt has been triggered for
// a user/server pair. Only records the first occurrence; subsequent calls for the
// same pair are no-ops so the original timestamp is preserved.
func (s *ssoTracker) MarkSSOPending(sub, serverName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingServers[sub] == nil {
		s.pendingServers[sub] = make(map[string]time.Time)
	}
	if _, exists := s.pendingServers[sub][serverName]; !exists {
		s.pendingServers[sub][serverName] = time.Now()
	}
}

// IsSSOPendingWithinTimeout returns true if SSO is pending AND the pending
// duration has not exceeded ssoTrackerPendingTimeout.
func (s *ssoTracker) IsSSOPendingWithinTimeout(sub, serverName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.pendingServers[sub]; ok {
		if firstSeen, exists := m[serverName]; exists {
			return time.Since(firstSeen) < ssoTrackerPendingTimeout
		}
	}
	return false
}

// ClearSSOPending removes the pending record for a user/server pair.
// Called when SSO completes (success or failure).
func (s *ssoTracker) ClearSSOPending(sub, serverName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.pendingServers[sub]; ok {
		delete(m, serverName)
		if len(m) == 0 {
			delete(s.pendingServers, sub)
		}
	}
}

// MarkSSOFailed records that SSO failed for a user/server pair. If an active
// (non-expired) failure entry already exists, the failure count is incremented
// to increase the exponential backoff. Otherwise a new entry is created with
// failureCount=1.
func (s *ssoTracker) MarkSSOFailed(sub, serverName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedServers[sub] == nil {
		s.failedServers[sub] = make(map[string]*ssoFailedEntry)
	}
	count := 1
	if prev, ok := s.failedServers[sub][serverName]; ok {
		prevBackoff := ssoBackoffDuration(prev.failureCount)
		if time.Since(prev.failedAt) < prevBackoff {
			count = prev.failureCount + 1
		}
	}
	s.failedServers[sub][serverName] = &ssoFailedEntry{
		failedAt:     time.Now(),
		failureCount: count,
	}
}

// HasSSOFailed returns true if SSO has recently failed for this user/server pair.
// The effective TTL increases with consecutive failures via exponential backoff:
// 1st failure → 5 min, 2nd → 10 min, 3rd → 20 min, capped at 30 min.
func (s *ssoTracker) HasSSOFailed(sub, serverName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.failedServers[sub]; ok {
		if entry, exists := m[serverName]; exists {
			return time.Since(entry.failedAt) < ssoBackoffDuration(entry.failureCount)
		}
	}
	return false
}

// GetFailureCount returns the number of consecutive SSO failures recorded for
// a user/server pair, or 0 if no active (non-expired) failure entry exists.
func (s *ssoTracker) GetFailureCount(sub, serverName string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.failedServers[sub]; ok {
		if entry, exists := m[serverName]; exists {
			if time.Since(entry.failedAt) < ssoBackoffDuration(entry.failureCount) {
				return entry.failureCount
			}
		}
	}
	return 0
}

// ClearSSOFailed removes the SSO failure record for a user/server pair.
func (s *ssoTracker) ClearSSOFailed(sub, serverName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.failedServers[sub]; ok {
		delete(m, serverName)
		if len(m) == 0 {
			delete(s.failedServers, sub)
		}
	}
}

// ClearAllSSOFailed removes all SSO failure records for a user.
// Called when the session is being re-initialized (e.g. after pod restart
// or session expiry) so that previously failed servers are retried.
func (s *ssoTracker) ClearAllSSOFailed(sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failedServers, sub)
}

// CleanupExpired removes SSO failure entries whose backoff window has elapsed.
// Each entry's effective TTL depends on its failure count (exponential backoff).
func (s *ssoTracker) CleanupExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub, servers := range s.failedServers {
		for serverName, entry := range servers {
			if time.Since(entry.failedAt) >= ssoBackoffDuration(entry.failureCount) {
				delete(servers, serverName)
			}
		}
		if len(servers) == 0 {
			delete(s.failedServers, sub)
		}
	}
}

// NewAggregatorServer creates a new aggregator server with the specified configuration.
//
// This constructor initializes all necessary components but does not start any servers.
// The returned server must be started with the Start method before it can handle requests.
//
// The server is configured with:
//   - A server registry using the specified muster prefix
//   - Active item managers for tracking capabilities
//   - Per-session capability cache and SSO tracker for OAuth servers
//   - Default transport settings based on configuration
//
// Args:
//   - aggConfig: Configuration args defining server behavior, transport, and security settings
//
// Returns a configured but unstarted aggregator server ready for initialization.
func NewAggregatorServer(aggConfig AggregatorConfig, errorCallback func(error)) *AggregatorServer {
	rateLimiter := NewAuthRateLimiter(DefaultAuthRateLimiterConfig())
	stores := createStores(aggConfig)

	return &AggregatorServer{
		config:          aggConfig,
		registry:        NewServerRegistry(aggConfig.MusterPrefix),
		toolManager:     newActiveItemManager(),
		errorCallback:   errorCallback,
		authRateLimiter: rateLimiter,
		authMetrics:     NewAuthMetrics(),
		authStore:       stores.authStore,
		capabilityStore: stores.capabilityStore,
		connPool:        NewSessionConnectionPool(DefaultConnectionPoolMaxAge),
		ssoTracker:      newSSOTracker(),
		subjectSessions: newSubjectSessionTracker(),
		valkeyClient:    stores.valkeyClient,
		valkeyKeyPrefix: stores.keyPrefix,
		valkeyEncryptor: stores.encryptor,
	}
}

// SetTransportDispatcher injects the CR-driven transport dispatcher and the
// controller-runtime client used to patch MCPServer status conditions. Both
// arguments may be nil — callers without Kubernetes (filesystem mode) leave
// them unset and the aggregator falls back to direct HTTPS (TB-7's
// "transport unset" path) without status updates. Idempotent; safe to call
// before Start.
func (a *AggregatorServer) SetTransportDispatcher(d teleport.TransportDispatcher, c ctrlclient.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transportDispatcher = d
	a.k8sClient = c
}

// resolveTransportClients invokes the configured TransportDispatcher with a
// synthesized v1alpha1.MCPServer carrying the CR's transport selection. When
// no dispatcher is configured (e.g. filesystem mode), returns a default
// http.Client and nil dexClient — the same shape as the dispatcher's
// "transport unset" branch (TB-7).
//
// On dispatcher error, this method patches
// MCPServer.status.conditions[type=TransportReady, status=False] with the
// reason from teleport.MapErrorToCondition. The patch is best-effort: a
// failure to write the condition is logged but never propagated to the
// caller (per PLAN: "the condition write should NOT block the request").
func (a *AggregatorServer) resolveTransportClients(ctx context.Context, info *ServerInfo) (mcpClient, dexClient *http.Client, err error) {
	if a.transportDispatcher == nil {
		return &http.Client{}, nil, nil
	}

	// Synthesize a v1alpha1.MCPServer. The dispatcher only consults
	// Spec.Transport; metadata is included so logs and errors carry the CR
	// identity for status writes.
	syn := &v1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      info.Name,
			Namespace: info.GetNamespace(),
		},
	}
	if info.TransportConfig != nil {
		syn.Spec.Transport = &v1alpha1.MCPServerTransport{
			Type: info.TransportConfig.Type,
		}
		if info.TransportConfig.Teleport != nil {
			syn.Spec.Transport.Teleport = &v1alpha1.TeleportTransport{
				Cluster: info.TransportConfig.Teleport.Cluster,
			}
		}
	}

	mcpClient, dexClient, err = a.transportDispatcher.ClientsFor(ctx, syn)
	if err != nil {
		a.writeTransportReadyCondition(ctx, syn, err)
		return nil, nil, err
	}
	a.writeTransportReadyCondition(ctx, syn, nil)
	return mcpClient, dexClient, nil
}

// writeTransportReadyCondition patches the TransportReady status condition on
// the named MCPServer. err==nil writes True/Ready; non-nil writes False with
// the reason returned by teleport.MapErrorToCondition. Best-effort — patch
// failures are logged and swallowed.
//
// Skips entirely when the controller-runtime client is unavailable (e.g.
// filesystem mode) or the CR does not declare a transport (no condition is
// meaningful in that case).
func (a *AggregatorServer) writeTransportReadyCondition(ctx context.Context, mcp *v1alpha1.MCPServer, dispatchErr error) {
	if a.k8sClient == nil || mcp == nil {
		return
	}
	if mcp.Spec.Transport == nil && dispatchErr == nil {
		// Don't spam the condition for direct-HTTPS CRs. TB-12 alerts only
		// fire on the failure path; a True condition is also unhelpful here.
		return
	}

	var (
		latest v1alpha1.MCPServer
		key    = apitypes.NamespacedName{Name: mcp.Name, Namespace: mcp.Namespace}
	)
	if err := a.k8sClient.Get(ctx, key, &latest); err != nil {
		// Filesystem-mode fakes / missing CRD; not an error worth warning on
		// repeatedly.
		if !meta.IsNoMatchError(err) {
			logging.DebugWithAttrs("Aggregator", "Skipping TransportReady condition patch (CR not gettable)",
				slog.String("server", mcp.Name),
				slog.String("error", err.Error()))
		}
		return
	}

	cond := metav1.Condition{
		Type:               "TransportReady",
		LastTransitionTime: metav1.Now(),
	}
	if dispatchErr == nil {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Resolved"
		cond.Message = "transport clients resolved"
	} else {
		reason, message := teleport.MapErrorToCondition(dispatchErr)
		cond.Status = metav1.ConditionFalse
		cond.Reason = reason
		cond.Message = message
	}

	// Only patch when the condition would actually change, to avoid
	// resourceVersion churn and conflict retries on every tool call.
	if !transportReadyChanged(latest.Status.Conditions, cond) {
		return
	}
	patch := ctrlclient.MergeFrom(latest.DeepCopy())
	setOrReplaceCondition(&latest.Status.Conditions, cond)
	if err := a.k8sClient.Status().Patch(ctx, &latest, patch); err != nil {
		logging.WarnWithAttrs("Aggregator", "Failed to patch MCPServer TransportReady condition",
			slog.String("server", mcp.Name),
			slog.String("namespace", mcp.Namespace),
			slog.String("error", err.Error()))
	}
}

// transportReadyChanged reports whether `next` would meaningfully replace any
// existing TransportReady condition in the slice.
func transportReadyChanged(existing []metav1.Condition, next metav1.Condition) bool {
	for _, c := range existing {
		if c.Type != next.Type {
			continue
		}
		return c.Status != next.Status || c.Reason != next.Reason || c.Message != next.Message
	}
	return true
}

// setOrReplaceCondition applies next to the slice, replacing any prior
// condition of the same Type. Minimal helper — does not introduce a new
// condition framework.
func setOrReplaceCondition(slice *[]metav1.Condition, next metav1.Condition) {
	for i, c := range *slice {
		if c.Type == next.Type {
			(*slice)[i] = next
			return
		}
	}
	*slice = append(*slice, next)
}

// storeBundle groups the results of createStores for readability.
type storeBundle struct {
	authStore       SessionAuthStore
	capabilityStore CapabilityStore
	valkeyClient    valkey.Client
	keyPrefix       string
	encryptor       *security.Encryptor
}

// createStores builds the session auth and capability stores based on the
// OAuthServer storage configuration. When the storage type is "valkey", a
// shared valkey.Client is created and both stores use it. Otherwise in-memory
// stores are returned.
func createStores(cfg AggregatorConfig) storeBundle {
	oauthCfg, ok := cfg.OAuthServer.Config.(config.OAuthServerConfig)
	if ok && oauthCfg.Storage.Type == "valkey" && oauthCfg.Storage.Valkey.URL != "" {
		keyPrefix := oauthCfg.Storage.Valkey.KeyPrefix
		if keyPrefix == "" {
			keyPrefix = "muster:" //nolint:goconst
		}

		client, err := newValkeyClient(oauthCfg.Storage.Valkey)
		if err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to create Valkey client for session stores, falling back to in-memory",
				slog.String("error", err.Error()))
			return storeBundle{
				authStore:       NewInMemorySessionAuthStore(DefaultCapabilityStoreTTL),
				capabilityStore: NewInMemoryCapabilityStore(DefaultCapabilityStoreTTL),
				keyPrefix:       keyPrefix,
			}
		}

		enc := createEncryptor(oauthCfg)

		logging.InfoWithAttrs("Aggregator", "Using Valkey-backed session auth and capability stores",
			slog.String("address", redactURL(oauthCfg.Storage.Valkey.URL)))
		return storeBundle{
			authStore:       NewValkeySessionAuthStore(client, DefaultCapabilityStoreTTL, keyPrefix),
			capabilityStore: NewValkeyCapabilityStore(client, DefaultCapabilityStoreTTL, keyPrefix),
			valkeyClient:    client,
			keyPrefix:       keyPrefix,
			encryptor:       enc,
		}
	}

	logging.Info("Aggregator", "Using in-memory session auth and capability stores")
	return storeBundle{
		authStore:       NewInMemorySessionAuthStore(DefaultCapabilityStoreTTL),
		capabilityStore: NewInMemoryCapabilityStore(DefaultCapabilityStoreTTL),
		keyPrefix:       "muster:",
	}
}

// createEncryptor builds an AES-256-GCM encryptor from the OAuthServerConfig
// encryption key. Returns nil if no key is configured or creation fails.
func createEncryptor(oauthCfg config.OAuthServerConfig) *security.Encryptor {
	if oauthCfg.EncryptionKey == "" {
		return nil
	}
	keyBytes, err := musteroauth.DecodeEncryptionKey(oauthCfg.EncryptionKey)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Failed to decode encryption key for Valkey stores",
			slog.String("error", err.Error()))
		return nil
	}
	enc, err := security.NewEncryptor(keyBytes)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Failed to create encryptor for Valkey stores",
			slog.String("error", err.Error()))
		return nil
	}
	if enc.IsEnabled() {
		logging.Info("Aggregator", "Token encryption at rest enabled for Valkey stores (AES-256-GCM)")
	}
	return enc
}

// newValkeyClient creates a valkey.Client from the shared ValkeyConfig.
func newValkeyClient(cfg config.ValkeyConfig) (valkey.Client, error) {
	opts := valkey.ClientOption{
		InitAddress: []string{cfg.URL},
	}
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	if cfg.DB != 0 {
		opts.SelectDB = cfg.DB
	}
	if cfg.TLSEnabled {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		if cfg.TLSServerName != "" {
			tlsCfg.ServerName = cfg.TLSServerName
		}
		opts.TLSConfig = tlsCfg
	}

	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("valkey connect: %w", err)
	}
	return client, nil
}

// redactURL removes any userinfo (user:password@) from a URL or address string
// before logging. Returns the input unchanged if it is not a parseable URL.
func redactURL(raw string) string {
	if !strings.Contains(raw, "@") {
		return raw
	}
	normalized := raw
	if !strings.Contains(raw, "://") {
		normalized = "redis://" + raw
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return raw
	}
	u.User = nil
	if !strings.Contains(raw, "://") {
		return u.Host + u.Path
	}
	return u.String()
}

// Start initializes and starts the aggregator server with all configured transports.
//
// This method performs a comprehensive startup sequence:
//  1. Creates and configures the core MCP server with full capabilities
//  2. Initializes workflow adapter if config directory is provided
//  3. Starts background monitoring of registry updates
//  4. Subscribes to tool update events from other muster components
//  5. Performs initial capability discovery and registration
//  6. Starts the appropriate transport server(s) based on configuration
//
// Transport Support:
//   - SSE: Server-Sent Events with HTTP endpoints (/sse, /message)
//   - Stdio: Standard input/output for CLI integration
//   - Streamable HTTP: HTTP-based streaming protocol (default)
//
// The method is idempotent for the same context - calling it multiple times with
// the same context will return an error indicating the server is already started.
//
// Args:
//   - ctx: Context for controlling the server lifecycle and coordinating shutdown
//
// Returns an error if startup fails at any stage, or nil on successful startup.
func (a *AggregatorServer) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.mcpServer != nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server already started")
	}

	// Create cancellable context for coordinating shutdown across all components
	a.ctx, a.cancelFunc = context.WithCancel(ctx)

	// Determine the server version to report
	serverVersion := a.config.Version
	if serverVersion == "" {
		serverVersion = "dev"
	}

	// Set up hooks for session lifecycle tracking and MCP protocol logging
	hooks := &mcpserver.Hooks{}
	hooks.AddOnUnregisterSession(func(_ context.Context, session mcpserver.ClientSession) {
		logging.InfoWithAttrs("MCP-Protocol", "Session unregistered",
			logging.TransportSessionID(session.SessionID()))
		a.subjectSessions.RemoveSession(session.SessionID())
	})

	hooks.AddOnRegisterSession(func(_ context.Context, session mcpserver.ClientSession) {
		logging.InfoWithAttrs("MCP-Protocol", "Session registered",
			logging.TransportSessionID(session.SessionID()))
	})

	hooks.AddAfterInitialize(func(ctx context.Context, _ any, msg *mcp.InitializeRequest, result *mcp.InitializeResult) {
		logging.InfoWithAttrs("MCP-Protocol", "Initialize completed",
			slog.String("client", msg.Params.ClientInfo.Name+"/"+msg.Params.ClientInfo.Version),
			slog.String("protocol", string(msg.Params.ProtocolVersion)),
			logging.TransportSessionID(getTransportSessionID(ctx)),
			slog.String("serverVersion", result.ServerInfo.Version))
	})

	hooks.AddAfterListTools(func(ctx context.Context, _ any, _ *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		toolNames := make([]string, 0, len(result.Tools))
		for _, t := range result.Tools {
			toolNames = append(toolNames, t.Name)
		}
		logging.InfoWithAttrs("MCP-Protocol", "tools/list response",
			logging.TransportSessionID(getTransportSessionID(ctx)),
			slog.String("subject", logging.TruncateIdentifier(getUserSubjectFromContext(ctx))),
			slog.Int("toolCount", len(result.Tools)),
			slog.Any("tools", toolNames))
	})

	hooks.AddBeforeCallTool(func(ctx context.Context, _ any, msg *mcp.CallToolRequest) {
		subject := getUserSubjectFromContext(ctx)
		logging.InfoWithAttrs("MCP-Protocol", "tools/call request",
			logging.TransportSessionID(getTransportSessionID(ctx)),
			slog.String("subject", logging.TruncateIdentifier(subject)),
			slog.String("tool", msg.Params.Name))

		// Ensure the admin UI can resolve subject for sessions whose MCP
		// client caches tools/list and therefore never re-hits
		// sessionToolFilter. TrackOAuth short-circuits under RLock when the
		// mapping is already recorded.
		a.subjectSessions.TrackOAuth(subject, getSessionIDFromContext(ctx))
	})

	hooks.AddAfterCallTool(func(ctx context.Context, _ any, msg *mcp.CallToolRequest, result any) {
		if r, ok := result.(*mcp.CallToolResult); ok {
			logging.InfoWithAttrs("MCP-Protocol", "tools/call response",
				logging.TransportSessionID(getTransportSessionID(ctx)),
				slog.String("tool", msg.Params.Name),
				slog.Bool("isError", r.IsError),
				slog.Int("contentItems", len(r.Content)))
		} else {
			logging.InfoWithAttrs("MCP-Protocol", "tools/call response",
				logging.TransportSessionID(getTransportSessionID(ctx)),
				slog.String("tool", msg.Params.Name),
				slog.String("resultType", fmt.Sprintf("%T", result)))
		}
	})

	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, _ any, err error) {
		logging.WarnWithAttrs("MCP-Protocol", "Error",
			logging.TransportSessionID(getTransportSessionID(ctx)),
			slog.String("method", string(method)),
			slog.Any("id", id),
			slog.String("error", err.Error()))
	})

	// Create MCP server with full capabilities enabled
	// WithToolFilter enables session-specific tool visibility for OAuth-authenticated servers
	// (see ADR-006: Session-Scoped Tool Visibility)
	mcpSrv := mcpserver.NewMCPServer(
		"muster-aggregator",
		serverVersion,
		mcpserver.WithToolCapabilities(true),           // Enable tool execution
		mcpserver.WithResourceCapabilities(true, true), // Enable resources with subscribe and listChanged
		mcpserver.WithPromptCapabilities(true),         // Enable prompt retrieval
		mcpserver.WithToolFilter(a.sessionToolFilter),  // Return session-specific tools for OAuth servers
		mcpserver.WithHooks(hooks),                     // Clean up subject-session mappings on disconnect
	)

	a.mcpServer = mcpSrv
	a.isShuttingDown = false

	// Start background monitoring for registry changes
	a.wg.Add(1)
	go a.monitorRegistryUpdates()

	// Start periodic cleanup for SSO failure entries
	a.wg.Add(1)
	go a.runSSOTrackerCleanup()

	// Subscribe to tool update events from workflow and other managers
	// This ensures the aggregator stays synchronized with core muster components
	logging.Info("Aggregator", "Subscribing to tool update events...")
	api.SubscribeToToolUpdates(a)
	logging.Info("Aggregator", "Successfully subscribed to tool update events")

	// Release the lock before calling updateCapabilities to avoid deadlock
	a.mu.Unlock()

	// Register the auth://status resource for exposing authentication state
	// This allows agents to poll for auth requirements and enable SSO detection
	// NOTE: Must be called after releasing lock since registerAuthStatusResource acquires RLock
	a.registerAuthStatusResource()

	// Register this aggregator as the MetaToolsDataProvider (Issue #343)
	// This enables the metatools package to access tools, resources, and prompts
	// through the aggregator for the server-side meta-tools migration.
	api.RegisterMetaToolsDataProvider(a)
	logging.Info("Aggregator", "Registered as MetaToolsDataProvider")

	// Perform initial capability discovery and registration
	a.updateCapabilities()

	// Start the configured transport server
	addr := fmt.Sprintf("%s:%d", a.config.Host, a.config.Port)

	// Check if we're running under systemd socket activation
	var systemdListeners []net.Listener = nil
	listenersWithNames, err := activation.ListenersWithNames()
	if err != nil {
		logging.Error("Aggregator", err, "Failed to get systemd listeners with names")
	} else {
		for name, listeners := range listenersWithNames {
			for i, l := range listeners {
				logging.InfoWithAttrs("Aggregator", "Systemd listener found",
					slog.Int("index", i), slog.String("name", name))
				systemdListeners = append(systemdListeners, l)
			}
		}
	}
	useSystemdActivation := len(systemdListeners) > 0
	if useSystemdActivation {
		logging.InfoWithAttrs("Aggregator", "Systemd socket activation detected",
			slog.Int("listeners", len(systemdListeners)))

		if a.config.Transport == config.MCPTransportStdio {
			return fmt.Errorf("stdio transport cannot be used with systemd socket activation")
		}
	}

	a.mu.Lock()

	switch a.config.Transport {
	case config.MCPTransportSSE:
		baseURL := fmt.Sprintf("http://%s:%d", a.config.Host, a.config.Port)
		a.sseServer = mcpserver.NewSSEServer(
			a.mcpServer,
			mcpserver.WithBaseURL(baseURL),
			mcpserver.WithSSEEndpoint("/sse"),               // Main SSE endpoint for events
			mcpserver.WithMessageEndpoint("/message"),       // Endpoint for sending messages
			mcpserver.WithKeepAlive(true),                   // Enable keep-alive for connection stability
			mcpserver.WithKeepAliveInterval(30*time.Second), // Keep-alive interval
		)

		// Create a mux that routes to both MCP and OAuth handlers
		handler, err := a.createHTTPMux(a.sseServer)
		if err != nil {
			return fmt.Errorf("failed to create HTTP mux with OAuth protection: %w", err)
		}

		if useSystemdActivation {
			logging.Info("Aggregator", "Using systemd socket activation for SSE transport")
			for i, listener := range systemdListeners {
				server := &http.Server{ //nolint:gosec
					Handler: handler,
				}
				a.httpServer = append(a.httpServer, server)
				go func(s *http.Server, l net.Listener, index int) {
					if err := s.Serve(l); err != nil && err != http.ErrServerClosed {
						logging.Error("Aggregator", err, "listener %d: SSE server error", index)
						a.errorCallback(err)
					}
				}(server, listener, i)
			}
		} else {
			logging.InfoWithAttrs("Aggregator", "Starting MCP aggregator server with SSE transport",
				slog.String("addr", addr))
			server := &http.Server{ //nolint:gosec
				Addr:    addr,
				Handler: handler,
			}
			a.httpServer = append(a.httpServer, server)
			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logging.Error("Aggregator", err, "SSE server error")
					a.errorCallback(err)
				}
			}()
		}

	case config.MCPTransportStdio:
		// Standard I/O transport for CLI integration
		logging.Info("Aggregator", "Starting MCP aggregator server with stdio transport")
		a.stdioServer = mcpserver.NewStdioServer(a.mcpServer)
		stdioServer := a.stdioServer
		if stdioServer != nil {
			// Same rationale as createStandardMux: inject a placeholder key so
			// downstream-auth flows have a session key for the capability store.
			stdioServer.SetContextFunc(func(ctx context.Context) context.Context {
				ctx = api.WithSubject(ctx, stdioDefaultUser)
				ctx = api.WithSessionID(ctx, stdioDefaultUser)
				return ctx
			})
			go func() {
				if err := stdioServer.Listen(a.ctx, os.Stdin, os.Stdout); err != nil {
					logging.Error("Aggregator", err, "Stdio server error")
					a.errorCallback(err)
				}
			}()
		}

	case config.MCPTransportStreamableHTTP:
		fallthrough
	default:
		// Streamable HTTP transport (default) - HTTP-based streaming protocol
		a.streamableHTTPServer = mcpserver.NewStreamableHTTPServer(a.mcpServer)

		// Create a mux that routes to both MCP and OAuth handlers
		handler, err := a.createHTTPMux(a.streamableHTTPServer)
		if err != nil {
			return fmt.Errorf("failed to create HTTP mux with OAuth protection: %w", err)
		}

		if useSystemdActivation {
			logging.Info("Aggregator", "Using systemd socket activation for streamable HTTP transport")
			for i, listener := range systemdListeners {
				server := &http.Server{ //nolint:gosec
					Handler: handler,
				}
				a.httpServer = append(a.httpServer, server)
				go func(s *http.Server, l net.Listener, index int) {
					if err := s.Serve(l); err != nil && err != http.ErrServerClosed {
						logging.Error("Aggregator", err, "listener %d: Streamable HTTP server error", index)
						a.errorCallback(err)
					}
				}(server, listener, i)
			}
		} else {
			logging.InfoWithAttrs("Aggregator", "Starting MCP aggregator server with streamable-http transport",
				slog.String("addr", addr))
			server := &http.Server{ //nolint:gosec
				Addr:    addr,
				Handler: handler,
			}
			a.httpServer = append(a.httpServer, server)
			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logging.Error("Aggregator", err, "Streamable HTTP server error")
					a.errorCallback(err)
				}
			}()
		}
	}
	a.mu.Unlock()

	// Start the optional admin web UI on a separate listener. Failure to
	// bind the admin port is logged but not fatal: the aggregator itself is
	// already serving and we do not want admin problems to take it down.
	if a.config.Admin.Enabled {
		adminCfg := admin.Config{
			BindAddress: a.config.Admin.BindAddress,
			Port:        a.config.Admin.Port,
		}
		adminSrv, err := admin.NewServer(adminCfg, a.adminDeps())
		if err != nil {
			logging.Error("Aggregator", err, "Failed to construct admin server (port %d)", adminCfg.Port)
		} else if err := adminSrv.Start(); err != nil {
			logging.Error("Aggregator", err, "Failed to start admin server on %s", adminSrv.Addr())
		} else {
			a.mu.Lock()
			a.adminServer = adminSrv
			a.mu.Unlock()
			logging.InfoWithAttrs("Aggregator", "Admin UI listening",
				slog.String("addr", adminSrv.Addr()))
		}
	}

	return nil
}

// Stop gracefully shuts down the aggregator server and all its components.
//
// This method performs a coordinated shutdown sequence:
//  1. Cancels the context to signal shutdown to all background routines
//  2. Shuts down all transport servers with a timeout
//  3. Waits for background routines to complete
//  4. Deregisters all backend servers to clean up connections
//  5. Resets internal state to allow for restart
//
// The shutdown process includes:
//   - Graceful shutdown of HTTP-based transports with a 5-second timeout
//   - Automatic shutdown of stdio transport via context cancellation
//   - Cleanup of all registered backend MCP servers
//   - Synchronization with background monitoring routines
//
// The method is idempotent - calling it multiple times is safe and will not
// cause errors or duplicate cleanup operations.
//
// Args:
//   - ctx: Context for controlling the shutdown timeout and operations
//
// Returns an error if shutdown encounters issues, though cleanup continues regardless.
func (a *AggregatorServer) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.isShuttingDown {
		a.mu.Unlock()
		return nil
	} else if a.mcpServer == nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server not started")
	}

	a.isShuttingDown = true // Prevent further updates during shutdown
	logging.Info("Aggregator", "Stopping MCP aggregator server")

	// Capture references before releasing lock to avoid race conditions
	cancelFunc := a.cancelFunc
	httpServer := a.httpServer
	adminServer := a.adminServer
	a.adminServer = nil
	a.mu.Unlock()

	// Shut down the admin listener first — it is cheap and has no in-flight
	// MCP work to wait for.
	if adminServer != nil {
		if err := adminServer.Stop(ctx); err != nil {
			logging.WarnWithAttrs("Aggregator", "Error shutting down admin server",
				slog.String("error", err.Error()))
		}
	}

	// Cancel context to signal shutdown to all background routines
	if cancelFunc != nil {
		cancelFunc()
	}

	// Shutdown transport servers with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Shutdown custom HTTP servers first (they take priority over MCP servers)
	if len(httpServer) > 0 {
		for _, s := range httpServer {
			if err := s.Shutdown(shutdownCtx); err != nil {
				logging.Error("Aggregator", err, "Error shutting down HTTP server")
			}
		}
	}

	// Note: Stdio server stops automatically on context cancellation, no explicit shutdown needed

	// Wait for all background routines to complete
	a.wg.Wait()

	// Stop the reaper and drain the connection pool before deregistering
	// servers so that pooled clients are closed cleanly.
	if a.connPool != nil {
		a.connPool.Stop()
		a.connPool.DrainAll()
	}

	// Clean up all registered backend servers
	for name := range a.registry.GetAllServers() {
		if err := a.registry.Deregister(name); err != nil {
			logging.WarnWithAttrs("Aggregator", "Error deregistering server",
				slog.String("server", name), slog.String("error", err.Error()))
		}
	}

	// Stop auth rate limiter background cleanup goroutine
	if a.authRateLimiter != nil {
		a.authRateLimiter.Stop()
	}

	// Stop auth store (cleans up timers for in-memory impl).
	if store, ok := a.authStore.(*InMemorySessionAuthStore); ok {
		store.Stop()
	}

	// Stop capability store (cleans up timers for in-memory impl).
	if store, ok := a.capabilityStore.(*InMemoryCapabilityStore); ok {
		store.Stop()
	}

	// Close shared Valkey client (if Valkey-backed stores were used).
	if a.valkeyClient != nil {
		a.valkeyClient.Close()
	}

	// Reset internal state to allow for clean restart
	a.mu.Lock()
	a.mcpServer = nil
	a.sseServer = nil
	a.streamableHTTPServer = nil
	a.stdioServer = nil
	a.httpServer = nil
	a.mu.Unlock()

	return nil
}

// RegisterServer registers a new backend MCP server with the aggregator.
//
// This method adds a backend MCP server to the aggregator's registry, making its
// tools, resources, and prompts available through the aggregated interface.
// The registration process includes client initialization and capability discovery.
//
// Args:
//   - ctx: Context for the registration operation and capability queries
//   - name: Unique identifier for the server within the aggregator
//   - client: MCP client interface for communicating with the backend server
//   - toolPrefix: Server-specific prefix for name collision resolution
//
// Returns an error if registration fails due to naming conflicts, client issues,
// or communication problems with the backend server.
func (a *AggregatorServer) RegisterServer(ctx context.Context, name string, client MCPClient, toolPrefix string) error {
	logging.DebugWithAttrs("Aggregator", "RegisterServer called",
		slog.String("server", name), slog.String("time", time.Now().Format("15:04:05.000")))

	// Wire the notification handler before registration so Initialize()
	// (called inside Register) forwards it to the underlying mcp-go client.
	client.OnNotification(func(notif mcp.JSONRPCNotification) {
		if isCapabilityNotification(notif.Method) {
			a.handleNonOAuthCapabilityChanged(name)
		}
	})

	return a.registry.Register(ctx, name, client, toolPrefix)
}

// wirePoolNotificationCallback sets up a notification callback on the
// connection pool so that whenever a new client is pooled for the given
// authenticated server, OnNotification is wired to listen for capability-change notifications.
func (a *AggregatorServer) wirePoolNotificationCallback(serverName string) {
	if a.connPool == nil {
		return
	}
	a.connPool.SetNotificationCallback(serverName, func(sessionID string, client MCPClient) {
		client.OnNotification(func(notif mcp.JSONRPCNotification) {
			if isCapabilityNotification(notif.Method) {
				a.handleSessionCapabilityChanged(serverName, sessionID, client)
			}
		})
	})
}

// DeregisterServer removes a backend MCP server from the aggregator.
//
// This method cleanly removes a backend server from the aggregator, which will
// cause all tools, resources, and prompts from that server to become unavailable.
// The backend client connection is closed as part of the deregistration process.
//
// Additionally, this method cleans up any stale session connections for the server.
// This is critical for handling MCPServer renames, where the old server is deleted
// and a new one is created. Without this cleanup, session connections stored under
// the old server name would persist and cause stale auth status displays.
//
// Args:
//   - name: Unique identifier of the server to remove
//
// Returns an error if the server is not found or deregistration fails.
func (a *AggregatorServer) DeregisterServer(name string) error {
	logging.DebugWithAttrs("Aggregator", "DeregisterServer called",
		slog.String("server", name), slog.String("time", time.Now().Format("15:04:05.000")))

	// Remove auth state and capabilities for this server across all sessions.
	if a.authStore != nil {
		if err := a.authStore.RevokeServer(context.Background(), name); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to revoke auth for server",
				slog.String("server", name), slog.String("error", err.Error()))
		}
	}
	if a.capabilityStore != nil {
		if err := a.capabilityStore.DeleteServer(context.Background(), name); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to delete server from capability store",
				slog.String("server", name), slog.String("error", err.Error()))
		}
	}

	// Evict all pooled connections for this server across all sessions.
	if a.connPool != nil {
		a.connPool.EvictServer(name)
	}

	return a.registry.Deregister(name)
}

// GetRegistry returns the server registry for direct access to backend server information.
//
// This method provides access to the underlying registry for advanced operations
// such as inspecting server status, accessing raw capabilities, or performing
// administrative tasks. It should be used carefully to avoid disrupting the
// aggregator's normal operation.
//
// Returns the ServerRegistry instance managing all backend servers.
func (a *AggregatorServer) GetRegistry() *ServerRegistry {
	return a.registry
}

// monitorRegistryUpdates runs a background monitoring loop for registry changes.
//
// This method continuously monitors the registry for changes (server registrations,
// deregistrations, or capability updates) and triggers appropriate responses:
//   - Updates the aggregator's exposed capabilities
//   - Publishes tool update events to notify dependent managers
//   - Maintains synchronization between backend servers and the aggregated interface
//
// The monitoring continues until the server's context is cancelled during shutdown.
// This method is designed to run as a background goroutine.
func (a *AggregatorServer) monitorRegistryUpdates() {
	defer a.wg.Done()

	updateChan := a.registry.GetUpdateChannel()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-updateChan:
			// Update server capabilities based on registered servers
			a.updateCapabilities()

			// Publish tool update event to trigger refresh in dependent managers
			a.publishToolUpdateEvent()
		}
	}
}

// runSSOTrackerCleanup periodically removes expired SSO failure entries.
func (a *AggregatorServer) runSSOTrackerCleanup() {
	defer a.wg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.ssoTracker.CleanupExpired()
		}
	}
}

// publishToolUpdateEvent publishes a tool update event to notify dependent managers.
//
// This method creates and publishes an event containing the current set of available
// tools, which notifies other muster components (like ServiceClass
// managers) that the tool landscape has changed. This ensures system-wide consistency
// when tools become available or unavailable.
//
// The event uses "aggregator" as the server name since it represents the aggregated
// view of all tools from multiple sources.
func (a *AggregatorServer) publishToolUpdateEvent() {
	// Get current tool inventory from all sources
	tools := a.GetAvailableTools()

	// Create and publish the tool update event
	event := api.ToolUpdateEvent{
		Type:       "tools_updated",
		ServerName: "aggregator", // Use aggregator as the source since it aggregates all tools
		Tools:      tools,
		Timestamp:  time.Now(),
	}

	// Publish the event - this will notify ServiceClass managers
	api.PublishToolUpdateEvent(event)

	logging.DebugWithAttrs("Aggregator", "Published tool update event",
		slog.Int("tools", len(tools)))
}

// updateCapabilities performs a comprehensive update of the aggregator's exposed capabilities.
//
// This method is the core of the aggregator's dynamic capability management. It:
//  1. Collects all available items from backend servers and core providers
//  2. Removes capabilities that are no longer available (cleanup)
//  3. Adds new capabilities that have become available
//  4. Updates the MCP server's advertised capabilities
//  5. Publishes events to notify dependent components
//
// The update process is designed to be efficient and minimize disruption to active
// connections. Items are added and removed in batches where possible, and the
// operation is thread-safe for concurrent access.
//
// This method is called:
//   - During server startup for initial capability discovery
//   - When backend servers are registered or deregistered
//   - When tool update events are received from core components
func (a *AggregatorServer) updateCapabilities() {
	a.mu.RLock()
	if a.mcpServer == nil {
		a.mu.RUnlock()
		return
	}
	a.mu.RUnlock()

	logging.Debug("Aggregator", "Updating capabilities dynamically")

	// Collect meta-tools once and pass to both remove/add to avoid
	// redundant createToolsFromProviders calls.
	metaTools := a.createToolsFromProviders()

	a.removeObsoleteMetaTools(metaTools)
	a.addNewMetaTools(metaTools)

	// Log summary of current capabilities
	servers := a.registry.GetAllServers()
	a.logCapabilitiesSummary(servers)

	// Publish tool update event to notify dependent managers (like ServiceClass manager)
	// This ensures subscribers are notified when core tools become available during startup
	a.publishToolUpdateEvent()
}

// removeObsoleteMetaTools removes meta-tools that are no longer provided.
// Downstream server tools are not registered on the MCP server; they are
// accessed through the call_tool meta-tool.
func (a *AggregatorServer) removeObsoleteMetaTools(metaTools []mcpserver.ServerTool) {
	currentTools := make(map[string]struct{}, len(metaTools))
	for _, tool := range metaTools {
		currentTools[tool.Tool.Name] = struct{}{}
	}

	obsolete := a.toolManager.getInactiveItems(currentTools)
	if len(obsolete) > 0 {
		logging.DebugWithAttrs("Aggregator", "Removing obsolete meta-tools",
			slog.Int("count", len(obsolete)), slog.Any("tools", obsolete))
		a.mcpServer.DeleteTools(obsolete...)
		a.toolManager.removeItems(obsolete)
	}
}

// addNewMetaTools registers meta-tools from core muster components on the MCP server.
//
// Only meta-tools (list_tools, call_tool, etc.) are registered on the MCP server.
// Downstream server tools are NOT registered -- they are accessed exclusively
// through the call_tool meta-tool which routes via CallToolInternal -> ServerRegistry.
//
// Only genuinely new tools (not already tracked by toolManager) are added to
// the MCP server. This prevents spurious tools/list_changed notifications that
// the mcp-go library sends automatically on every AddTools call.
func (a *AggregatorServer) addNewMetaTools(metaTools []mcpserver.ServerTool) {
	var newTools []mcpserver.ServerTool
	for _, tool := range metaTools {
		if !a.toolManager.isActive(tool.Tool.Name) {
			newTools = append(newTools, tool)
		}
	}

	if len(newTools) > 0 {
		logging.DebugWithAttrs("Aggregator", "Adding new meta-tools in batch",
			slog.Int("count", len(newTools)))
		a.mcpServer.AddTools(newTools...)
		for _, tool := range newTools {
			a.toolManager.track(tool.Tool.Name)
		}
	}
}

// logCapabilitiesSummary logs a comprehensive summary of current capabilities.
//
// This method provides diagnostic information about the current state of the
// aggregator by counting and logging the total number of tools, resources,
// and prompts available from all connected backend servers.
//
// The summary helps with:
//   - Monitoring aggregator health and functionality
//   - Debugging capability discovery issues
//   - Understanding the current tool landscape
//
// Args:
//   - servers: Map of all registered backend servers for capability counting
func (a *AggregatorServer) logCapabilitiesSummary(servers map[string]*ServerInfo) {
	toolCount := 0
	resourceCount := 0
	promptCount := 0

	for _, info := range servers {
		if info.IsConnected() {
			info.mu.RLock()
			toolCount += len(info.Tools)
			resourceCount += len(info.Resources)
			promptCount += len(info.Prompts)
			info.mu.RUnlock()
		}
	}

	logging.DebugWithAttrs("Aggregator", "Updated capabilities",
		slog.Int("tools", toolCount), slog.Int("resources", resourceCount), slog.Int("prompts", promptCount))
}

// createHTTPMux creates an HTTP mux that routes to both MCP and OAuth handlers.
// This allows the aggregator to serve both MCP protocol traffic and OAuth callbacks
// on the same port.
//
// If OAuth server protection is enabled (OAuthServer.Enabled), the MCP handler is
// wrapped with OAuth ValidateToken middleware, requiring valid access tokens for
// all MCP requests.
//
// Returns an error if OAuth is enabled but cannot be initialized (security requirement).
func (a *AggregatorServer) createHTTPMux(mcpHandler http.Handler) (http.Handler, error) {
	// Check if OAuth server protection is enabled
	if a.config.OAuthServer.Enabled && a.config.OAuthServer.Config != nil {
		return a.createOAuthProtectedMux(mcpHandler)
	}

	// Standard mux without OAuth server protection
	return a.createStandardMux(mcpHandler), nil
}

// createStandardMux creates a standard HTTP mux without OAuth server protection.
// This is used when OAuth server protection is disabled.
//
// Since there is no OAuth middleware to set session/subject in context, this
// injects stdioDefaultUser as a single-user identity (same as stdio transport).
func (a *AggregatorServer) createStandardMux(mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint for Kubernetes probes
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Check if OAuth proxy is enabled and mount OAuth-related handlers (for downstream auth)
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() {
		// Mount the OAuth callback handler
		callbackPath := oauthHandler.GetCallbackPath()
		if callbackPath != "" {
			mux.Handle(callbackPath, oauthHandler.GetHTTPHandler())
			logging.InfoWithAttrs("Aggregator", "Mounted OAuth callback handler",
				slog.String("path", callbackPath))
		}

		// Mount the CIMD handler if self-hosting is enabled
		if oauthHandler.ShouldServeCIMD() {
			cimdPath := oauthHandler.GetCIMDPath()
			cimdHandler := oauthHandler.GetCIMDHandler()
			if cimdPath != "" && cimdHandler != nil {
				mux.HandleFunc(cimdPath, cimdHandler)
				logging.InfoWithAttrs("Aggregator", "Mounted self-hosted CIMD",
					slog.String("path", cimdPath))
			}
		}
	}

	// Without OAuth, there is no ValidateToken middleware to set session/subject.
	// Inject stdioDefaultUser so that downstream-auth flows (core_auth_login)
	// have a key for the session-scoped capability store and connection pool.
	// Servers that don't require session auth never use this — they go through the global client.
	defaultUserMCPHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := api.WithSubject(r.Context(), stdioDefaultUser)
		ctx = api.WithSessionID(ctx, stdioDefaultUser)
		mcpHandler.ServeHTTP(w, r.WithContext(ctx))
	})

	mux.Handle("/", defaultUserMCPHandler)

	return mux
}

// createOAuthProtectedMux creates an HTTP mux with OAuth 2.1 protection.
// All MCP endpoints are protected by the ValidateToken middleware.
//
// SECURITY: This function returns an error instead of silently falling back to
// an unprotected mux. When OAuth is enabled, authentication MUST work - running
// without authentication is a security vulnerability.
func (a *AggregatorServer) createOAuthProtectedMux(mcpHandler http.Handler) (http.Handler, error) {
	// Import the config type and create OAuth HTTP server
	cfg, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig)
	if !ok {
		return nil, fmt.Errorf("invalid OAuth server config type: expected OAuthServerConfig")
	}

	oauthHTTPServer, err := server.NewOAuthHTTPServer(cfg, mcpHandler, a.config.Debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth HTTP server: %w", err)
	}

	// Store the OAuth HTTP server for cleanup during shutdown
	a.oauthHTTPServer = oauthHTTPServer

	// On every authenticated request, extend the capability store TTL.
	// If the session has expired (e.g. user returns after inactivity, or
	// Valkey was restarted), re-establish SSO connections in the background.
	// Also detects broken upstream refresh chains: when authAlive is true but
	// the ID token has disappeared, SSO connections are evicted to stop the
	// mcp-go retry loop from spamming errors with expired tokens.
	oauthHTTPServer.SetOnAuthenticated(func(ctx context.Context, sessionID string) {
		if a.capabilityStore == nil {
			return
		}
		var authAlive bool
		if a.authStore != nil {
			authAlive, _ = a.authStore.Touch(ctx, sessionID)
		}
		_, _ = a.capabilityStore.Touch(ctx, sessionID)

		userID := getUserSubjectFromContext(ctx)
		idToken, _ := server.GetIDTokenFromContext(ctx)

		logging.InfoWithAttrs("Aggregator", "SSO: onAuthenticated callback",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("userID", logging.TruncateIdentifier(userID)),
			slog.Bool("authAlive", authAlive),
			slog.Bool("hasIDToken", idToken != ""))

		if authAlive {
			if idToken == "" {
				// Session is known but the ID token is gone: the upstream
				// refresh chain is broken (e.g. Dex -> GitHub returned 401).
				// Evict stale SSO connections to stop mcp-go's infinite
				// 1-second retry loop.
				a.handleUpstreamRefreshFailure(sessionID, userID, "onAuthenticated: ID token missing for active session")
			}
			return
		}

		// Don't initiate SSO for stale sessions without a usable ID token.
		// After a pod restart, Valkey may still have valid access tokens but no ID token
		// in the OAuth store. Without this gate, downstream connections are established
		// that immediately start spamming 403 errors.
		if idToken == "" {
			logging.InfoWithAttrs("Aggregator", "SSO: skipping initSSOForSession, no ID token available (stale session after restart)",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
			return
		}

		// Clear prior SSO failures so all servers are retried. Without this,
		// servers that failed during a previous init (e.g. before a pod
		// restart or after a transient token-storage error) would be skipped
		// for ssoTrackerFailureTTL even though the underlying cause may have
		// been resolved.
		if a.ssoTracker != nil && userID != "" {
			a.ssoTracker.ClearAllSSOFailed(userID)
		}

		go a.initSSOForSession(ctx, userID, sessionID, idToken) //nolint:gosec
	})

	// Establish SSO connections synchronously during login (token issuance).
	// This fires inside ExchangeAuthorizationCode, so SSO servers are connected
	// before the client receives its access token.
	oauthServer := oauthHTTPServer.GetOAuthServer()

	oauthServer.SetSessionCreationHandler(func(ctx context.Context, userID, familyID string, token *oauth2.Token) {
		idToken := oauthserver.ExtractIDToken(token)
		logging.InfoWithAttrs("Aggregator", "SSO: SessionCreationHandler fired",
			slog.String("userID", logging.TruncateIdentifier(userID)),
			slog.String("familyID", logging.TruncateIdentifier(familyID)),
			slog.Bool("hasIDToken", idToken != ""),
			slog.Int("idTokenLen", len(idToken)))
		a.initSSOForSession(ctx, userID, familyID, idToken)
		a.storeIDTokenForSSO(familyID, userID, idToken)
	})

	oauthServer.SetTokenRefreshHandler(func(ctx context.Context, userID, familyID string, newToken *oauth2.Token) {
		idToken := oauthserver.ExtractIDToken(newToken)
		if idToken == "" {
			// The upstream provider refreshed tokens but did not include an
			// ID token. This signals a degraded refresh chain (e.g. Dex
			// obtained new access/refresh tokens from GitHub but the OIDC
			// id_token was dropped). Evict SSO connections so they don't
			// keep retrying with stale credentials.
			a.handleUpstreamRefreshFailure(familyID, userID, "TokenRefreshHandler: refreshed token has no ID token")
			return
		}
		a.storeIDTokenForSSO(familyID, userID, idToken)
		logging.DebugWithAttrs("Aggregator", "Stored refreshed ID token via TokenRefreshHandler",
			slog.String("familyID", logging.TruncateIdentifier(familyID)))
	})

	oauthServer.SetSessionRevocationHandler(func(ctx context.Context, userID, familyID string) {
		a.tearDownSession(ctx, familyID)
		if oauthHandler := api.GetOAuthHandler(); oauthHandler != nil && oauthHandler.IsEnabled() {
			oauthHandler.DeleteTokensBySession(familyID)
		}
		logging.InfoWithAttrs("Aggregator", "Cleaned up session state for revoked session",
			slog.String("familyID", logging.TruncateIdentifier(familyID)),
			slog.String("userID", logging.TruncateIdentifier(userID)))
	})

	logging.InfoWithAttrs("Aggregator", "OAuth 2.1 server protection enabled",
		slog.String("baseURL", cfg.BaseURL))

	oauthMux := oauthHTTPServer.CreateMux()
	outerMux := http.NewServeMux()

	// Authenticated logout endpoints (behind ValidateToken middleware).
	// These require a valid Bearer token and extract the user's subject from context.
	outerMux.Handle("DELETE /user-tokens", oauthHTTPServer.ValidateTokenWithSubject(
		http.HandlerFunc(a.handleUserTokensDeletion)))
	outerMux.Handle("DELETE /auth/{server}", oauthHTTPServer.ValidateTokenWithSubject(
		http.HandlerFunc(a.handleAuthServerDeletion)))

	outerMux.Handle("/", oauthMux)

	return outerMux, nil
}

// GetEndpoint returns the aggregator's primary endpoint URL based on the configured transport.
//
// The endpoint format varies by transport type:
//   - SSE: http://host:port/sse (Server-Sent Events endpoint)
//   - Streamable HTTP: http://host:port/mcp (default HTTP streaming path)
//   - Stdio: "stdio" (indicates standard I/O communication)
//
// This endpoint can be used by MCP clients to connect to the aggregator and
// access all aggregated capabilities from backend servers.
//
// Returns the endpoint URL as a string, or "stdio" for standard I/O transport.
func (a *AggregatorServer) GetEndpoint() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	switch a.config.Transport {
	case config.MCPTransportSSE:
		return fmt.Sprintf("http://%s:%d/sse", a.config.Host, a.config.Port)
	case config.MCPTransportStreamableHTTP:
		return fmt.Sprintf("http://%s:%d/mcp", a.config.Host, a.config.Port) // Default path for streamable
	case config.MCPTransportStdio:
		return "stdio"
	default:
		// Default to streamable-http endpoint
		return fmt.Sprintf("http://%s:%d/mcp", a.config.Host, a.config.Port)
	}
}

// GetTools returns all available tools from all sources with intelligent name prefixing.
//
// This method aggregates tools from all registered backend servers, applying
// smart prefixing to avoid name conflicts. The prefixing is only applied when
// conflicts would otherwise occur, following the pattern:
// {muster_prefix}_{server_prefix}_{original_name}
//
// Note: This returns the global tool view. For session-specific tool visibility,
// use GetToolsForSession instead.
//
// Returns a slice of MCP tools ready for client consumption.
func (a *AggregatorServer) GetTools() []mcp.Tool {
	return a.registry.GetAllTools()
}

// GetToolsForSession returns a session-specific view of all available tools.
// For OAuth servers, tools are read from the CapabilityStore keyed by session ID.
// For non-OAuth servers, tools are read from ServerInfo (same as GetAllTools).
func (a *AggregatorServer) GetToolsForSession(ctx context.Context, sessionID string) []mcp.Tool {
	return a.registry.GetAllToolsForSession(ctx, a.capabilityStore, sessionID)
}

// GetResourcesForSession returns a session-specific view of all available resources.
// For OAuth servers, resources are read from the CapabilityStore keyed by session ID.
func (a *AggregatorServer) GetResourcesForSession(ctx context.Context, sessionID string) []mcp.Resource {
	return a.registry.GetAllResourcesForSession(ctx, a.capabilityStore, sessionID)
}

// GetPromptsForSession returns a session-specific view of all available prompts.
// For OAuth servers, prompts are read from the CapabilityStore keyed by session ID.
func (a *AggregatorServer) GetPromptsForSession(ctx context.Context, sessionID string) []mcp.Prompt {
	return a.registry.GetAllPromptsForSession(ctx, a.capabilityStore, sessionID)
}

// sessionToolFilter is the WithToolFilter callback for MCP tools/list.
//
// This filter ensures that MCP tools/list only returns the meta-tools
// (list_tools, call_tool, etc.) -- NOT downstream server tools. Downstream
// tools are accessible exclusively through the call_tool meta-tool, which
// routes through CallToolInternal -> ServerRegistry.
//
// This prevents the client from seeing 200+ tools and receiving constant
// tools/list_changed notifications as SSO connections are established.
func (a *AggregatorServer) sessionToolFilter(ctx context.Context, _ []mcp.Tool) []mcp.Tool {
	subject := getUserSubjectFromContext(ctx)

	// Track subject -> MCP session mapping for targeted notifications.
	// Skip tracking when subject is empty to avoid polluting the session map.
	if subject != "" {
		if session := mcpserver.ClientSessionFromContext(ctx); session != nil {
			a.subjectSessions.Track(subject, session.SessionID())
		}
		// Also record the OAuth session ID (token family) so the admin UI
		// can enumerate live sessions that haven't touched the cap store yet.
		if oauthSessionID := getSessionIDFromContext(ctx); oauthSessionID != "" {
			a.subjectSessions.TrackOAuth(subject, oauthSessionID)
		}
	}

	// Only return meta-tools -- downstream tools are accessed via call_tool
	coreServerTools := a.createToolsFromProviders()
	allTools := make([]mcp.Tool, 0, len(coreServerTools))
	for _, serverTool := range coreServerTools {
		allTools = append(allTools, serverTool.Tool)
	}

	logging.InfoWithAttrs("MCP-Protocol", "sessionToolFilter: returning meta-tools",
		slog.Int("toolCount", len(allTools)),
		slog.String("subject", logging.TruncateIdentifier(subject)),
		logging.TransportSessionID(getTransportSessionID(ctx)))

	return allTools
}

// GetToolsWithStatus returns all available tools along with their security blocking status.
//
// This method provides enhanced tool information that includes whether each tool
// is blocked by the security denylist. The blocking status depends on:
//   - The tool's classification as destructive in the denylist
//   - The current yolo mode setting (yolo=true allows all tools)
//
// The tool names are resolved to their original names (before prefixing) for
// accurate denylist checking, ensuring consistent security behavior regardless
// of how tools are exposed.
//
// Returns a slice of ToolWithStatus containing both tool definitions and security status.
func (a *AggregatorServer) GetToolsWithStatus() []ToolWithStatus {
	a.mu.RLock()
	yolo := a.config.Yolo
	a.mu.RUnlock()

	tools := a.registry.GetAllTools()
	result := make([]ToolWithStatus, 0, len(tools))

	for _, tool := range tools {
		// Resolve the tool to get the original name for accurate denylist checking
		var originalName string
		if _, origName, err := a.registry.ResolveToolName(tool.Name); err == nil {
			originalName = origName
		} else {
			// If we can't resolve, use the exposed name as fallback
			originalName = tool.Name
		}

		result = append(result, ToolWithStatus{
			Tool:    tool,
			Blocked: !yolo && isDestructiveTool(originalName),
		})
	}

	return result
}

// GetResources returns all available resources from all registered backend servers.
//
// This method aggregates resources from all connected backend servers, applying
// appropriate URI prefixing to avoid conflicts. Resources maintain their original
// functionality while being properly namespaced within the aggregated environment.
//
// Returns a slice of MCP resources ready for client access.
func (a *AggregatorServer) GetResources() []mcp.Resource {
	return a.registry.GetAllResources()
}

// GetPrompts returns all available prompts from all sources with intelligent name prefixing.
//
// This method aggregates prompts from all registered backend servers, applying
// smart prefixing similar to tools to avoid name conflicts. The prefixing ensures
// that prompts from different servers can coexist without naming collisions.
//
// Returns a slice of MCP prompts ready for client consumption.
func (a *AggregatorServer) GetPrompts() []mcp.Prompt {
	return a.registry.GetAllPrompts()
}

// ToggleToolBlock toggles the blocked status of a specific tool (placeholder implementation).
//
// This method is intended to provide runtime control over individual tool blocking,
// allowing administrators to override the default denylist behavior for specific tools.
// Currently, this functionality is not fully implemented and returns an error.
//
// Future Enhancement:
// The full implementation would maintain a runtime override list that could
// selectively enable or disable specific tools regardless of the global yolo setting.
//
// Args:
//   - toolName: Name of the tool to toggle blocking status for
//
// Returns an error indicating the feature is not yet implemented.
func (a *AggregatorServer) ToggleToolBlock(toolName string) error {
	// For now, we can only toggle between fully enabled (yolo) or default denylist
	// In a future enhancement, we could maintain a runtime override list
	// For now, we just return an error indicating this needs more work
	return fmt.Errorf("individual tool blocking toggle not yet implemented")
}

// IsYoloMode returns whether yolo mode is currently enabled.
//
// Yolo mode disables the security denylist, allowing all tools to be executed
// regardless of their destructive potential. This mode should only be enabled
// in development or testing environments where the risk is acceptable.
//
// Returns true if yolo mode is enabled, false if security filtering is active.
func (a *AggregatorServer) IsYoloMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Yolo
}

// CallToolInternal provides internal tool calling capability for muster components.
//
// This method allows internal muster components to execute tools through the
// aggregator without going through the external MCP protocol. It supports both:
//   - Tools from registered backend servers (resolved through the registry)
//   - Core tools from muster components (called directly through providers)
//
// The method performs intelligent tool resolution:
//  1. First attempts to resolve the tool through the server registry
//  2. If not found, checks if it's a core tool by name pattern
//  3. Routes the call to the appropriate handler based on tool type
//
// This internal calling mechanism is essential for:
//   - Inter-component communication within muster
//   - Workflow execution that needs to call other tools
//   - Administrative operations that require tool access
//
// Args:
//   - ctx: Context for the tool execution
//   - toolName: Name of the tool to execute (may be prefixed)
//   - args: Arguments to pass to the tool as key-value pairs
//
// Returns the tool execution result or an error if the tool cannot be found or executed.
func (a *AggregatorServer) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.DebugWithAttrs("Aggregator", "CallToolInternal called",
		slog.String("tool", toolName))

	sub := getUserSubjectFromContext(ctx)
	sessionID := getSessionIDFromContext(ctx)

	serverName, originalName, err := a.registry.ResolveToolName(toolName)
	if err == nil {
		logging.DebugWithAttrs("Aggregator", "Tool found in registry",
			slog.String("tool", toolName), slog.String("server", serverName), slog.String("original", originalName))
		serverInfo, exists := a.registry.GetServerInfo(serverName)
		if !exists || serverInfo == nil {
			return nil, fmt.Errorf("server not found: %s", serverName)
		}

		if !serverInfo.RequiresSessionAuth() && serverInfo.Client != nil {
			logging.DebugWithAttrs("Aggregator", "Using global client",
				slog.String("server", serverName))
			return serverInfo.Client.CallTool(ctx, originalName, args)
		}

		if serverInfo.RequiresSessionAuth() {
			if sessionID == "" {
				logging.WarnWithAttrs("Aggregator", "Tool requires auth but no session ID in context. "+
					"The OAuth middleware may not have propagated the session — check createAccessTokenInjectorMiddleware.",
					slog.String("tool", toolName),
					slog.String("server", serverName))
				return nil, fmt.Errorf("tool %s requires authentication but no session is available", toolName)
			}
			logging.DebugWithAttrs("Aggregator", "Server requires auth, trying on-demand client",
				slog.String("server", serverName), slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
			_, sessionOriginalName, sessionErr := a.resolveUserTool(sessionID, toolName)
			if sessionErr == nil {
				logging.DebugWithAttrs("Aggregator", "Using on-demand client",
					slog.String("tool", toolName))
				return a.callToolWithTokenExchangeRetry(ctx, serverName, sessionOriginalName, args, sessionID, sub)
			}
			logging.DebugWithAttrs("Aggregator", "No cached capabilities found",
				slog.String("tool", toolName), slog.String("error", sessionErr.Error()))
		}

		if serverInfo.Client == nil {
			return nil, fmt.Errorf("server not connected: %s (status: %s)", serverName, serverInfo.GetStatus())
		}

		return serverInfo.Client.CallTool(ctx, originalName, args)
	}

	logging.DebugWithAttrs("Aggregator", "Tool not found in registry, checking capability cache",
		slog.String("tool", toolName), slog.String("error", err.Error()))

	if sessionID != "" {
		sessionServerName, originalName, sessionErr := a.resolveUserTool(sessionID, toolName)
		if sessionErr == nil {
			logging.DebugWithAttrs("Aggregator", "Tool found in capability cache",
				slog.String("tool", toolName), slog.String("server", sessionServerName))
			return a.callToolWithTokenExchangeRetry(ctx, sessionServerName, originalName, args, sessionID, sub)
		}
	}

	logging.DebugWithAttrs("Aggregator", "Tool not found in registry or cache, checking core tools",
		slog.String("tool", toolName))

	// If not found in registry or session, check if it's a core tool by name pattern
	// This avoids the deadlock that can occur when calling createToolsFromProviders()
	// during workflow execution
	if a.isCoreToolByName(toolName) {
		logging.DebugWithAttrs("Aggregator", "Tool matches core tool pattern, calling directly",
			slog.String("tool", toolName))
		return a.callCoreToolDirectly(ctx, toolName, args)
	}

	logging.DebugWithAttrs("Aggregator", "Tool not found in registry, session, or core tools",
		slog.String("tool", toolName))
	return nil, fmt.Errorf("tool not found: %s", toolName)
}

// isCoreToolByName checks if a tool name matches the pattern of core tools
// without needing to recreate the tool list (which can cause deadlocks)
func (a *AggregatorServer) isCoreToolByName(toolName string) bool {
	// Core tools have specific naming patterns based on their prefix
	coreToolPrefixes := []string{
		"core_workflow_",
		"core_service_",
		"core_config_",
		"core_serviceclass_",
		"core_mcpserver_",
		"core_events",
		"core_auth_", // Authentication tools (core_auth_login, core_auth_logout)
		"workflow_",  // Direct workflow execution tools
	}

	for _, prefix := range coreToolPrefixes {
		if strings.HasPrefix(toolName, prefix) {
			return true
		}
	}

	return false
}

// callCoreToolDirectly routes core tool calls to the appropriate muster component providers.
//
// This method handles the execution of core muster tools that are not provided by
// external backend servers but rather by internal muster components. It performs
// intelligent routing based on tool name prefixes to determine which component
// should handle the tool execution.
//
// Tool Routing Logic:
//   - workflow_*: Routed to the workflow manager for workflow operations
//   - service_*: Routed to the service manager for service lifecycle operations
//   - config_*: Routed to the config manager for configuration operations
//   - serviceclass_*: Routed to the service class manager for service class operations
//   - mcpserver_*: Routed to the MCP server manager for MCP server operations
//
// The method removes the "core_" prefix from tool names before routing to ensure
// proper tool resolution within each component's tool provider interface.
//
// Args:
//   - ctx: Context for the tool execution
//   - toolName: Name of the core tool to execute (with core_ prefix)
//   - args: Arguments to pass to the tool as key-value pairs
//
// Returns the tool execution result converted to MCP format, or an error if
// no appropriate handler is found or execution fails.
func (a *AggregatorServer) callCoreToolDirectly(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.DebugWithAttrs("Aggregator", "callCoreToolDirectly called",
		slog.String("tool", toolName))

	// Remove the core_ prefix to get the original tool name for routing
	originalToolName := strings.TrimPrefix(toolName, "core_")
	logging.DebugWithAttrs("Aggregator", "Original tool name after prefix removal",
		slog.String("tool", originalToolName))

	// Route to the appropriate provider based on tool name prefix
	switch {
	case strings.HasPrefix(originalToolName, "workflow_"):
		// Workflow management and execution tools
		handler := api.GetWorkflow()
		if handler == nil {
			return nil, fmt.Errorf("workflow handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			// Check if this is a workflow management tool or a workflow execution tool
			managementTools := []string{"workflow_list", "workflow_get", "workflow_create",
				"workflow_update", "workflow_delete", "workflow_validate", "workflow_available",
				"workflow_execution_list", "workflow_execution_get"}

			isManagementTool := false
			for _, mgmtTool := range managementTools {
				if originalToolName == mgmtTool {
					isManagementTool = true
					break
				}
			}

			if isManagementTool {
				// Use the original tool name for workflow management tools
				logging.DebugWithAttrs("Aggregator", "Calling workflow management tool directly",
					slog.String("tool", originalToolName))
				result, err := provider.ExecuteTool(ctx, originalToolName, args)
				if err != nil {
					return nil, err
				}
				return convertToMCPResult(result), nil
			} else {
				// This is a workflow execution tool - map workflow_ to action_
				actionToolName := strings.Replace(originalToolName, "workflow_", "action_", 1)
				logging.DebugWithAttrs("Aggregator", "Mapping workflow execution tool to action tool",
					slog.String("tool", originalToolName), slog.String("actionTool", actionToolName))
				result, err := provider.ExecuteTool(ctx, actionToolName, args)
				if err != nil {
					return nil, err
				}
				return convertToMCPResult(result), nil
			}
		}
		return nil, fmt.Errorf("workflow handler does not implement ToolProvider interface")

	case strings.HasPrefix(originalToolName, "service_"):
		// Service lifecycle management operations
		handler := api.GetServiceManager()
		if handler == nil {
			return nil, fmt.Errorf("service manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}
		return nil, fmt.Errorf("service manager does not implement ToolProvider interface")

	case strings.HasPrefix(originalToolName, "config_"):
		// Configuration management operations
		handler := api.GetConfig() //nolint:staticcheck
		if handler == nil {
			return nil, fmt.Errorf("config handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}
		return nil, fmt.Errorf("config handler does not implement ToolProvider interface")

	case strings.HasPrefix(originalToolName, "serviceclass_"):
		// Service class management operations
		handler := api.GetServiceClassManager()
		if handler == nil {
			return nil, fmt.Errorf("service class manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}
		return nil, fmt.Errorf("service class manager does not implement ToolProvider interface")

	case strings.HasPrefix(originalToolName, "mcpserver_"):
		// MCP server management operations
		handler := api.GetMCPServerManager()
		if handler == nil {
			return nil, fmt.Errorf("MCP server manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			mcpResult := convertToMCPResult(result)

			if originalToolName == "mcpserver_list" {
				mcpResult = a.enrichServerList(ctx, mcpResult)
			}

			return mcpResult, nil
		}
		return nil, fmt.Errorf("MCP server manager does not implement ToolProvider interface")

	case originalToolName == "events":
		// Event management operations
		handler := api.GetEventManager()
		if handler == nil {
			return nil, fmt.Errorf("event manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}
		return nil, fmt.Errorf("event manager does not implement ToolProvider interface")

	case strings.HasPrefix(originalToolName, "auth_"):
		// Authentication operations (auth_login, auth_logout)
		authProvider := NewAuthToolProvider(a)
		result, err := authProvider.ExecuteTool(ctx, originalToolName, args)
		if err != nil {
			return nil, err
		}
		return convertToMCPResult(result), nil

	default:
		return nil, fmt.Errorf("no handler found for core tool: %s", originalToolName)
	}
}

// IsToolAvailable implements the ToolAvailabilityChecker interface.
//
// This method determines whether a specific tool is available through the aggregator,
// checking both external backend servers (via the registry) and core muster tools
// (via name pattern matching). It provides a unified way for other components to
// verify tool availability before attempting to use them.
//
// The check process:
//  1. Attempts to resolve the tool through the server registry
//  2. If not found, checks if it matches core tool naming patterns
//  3. Returns true if found in either location
//
// This method is used by:
//   - Workflow manager for validating workflow step tools
//   - Service class manager for tool availability validation
//
// Args:
//   - toolName: Name of the tool to check (may be prefixed)
//
// Returns true if the tool is available, false otherwise.
func (a *AggregatorServer) IsToolAvailable(toolName string) bool {
	// Check if the tool exists in any registered backend server
	_, _, err := a.registry.ResolveToolName(toolName)
	if err == nil {
		return true // Found in registry
	}

	// Check if it's a core tool by name pattern (avoid deadlock)
	if a.isCoreToolByName(toolName) {
		return true // Found in core tools
	}

	return false // Not found anywhere
}

// GetAvailableTools implements the ToolAvailabilityChecker interface.
//
// This method returns a comprehensive list of all tools currently available
// through the aggregator, including both external tools from backend servers
// and core tools from muster components. The returned list represents the
// complete tool inventory that can be used by workflows, capabilities, and
// other muster components.
//
// The aggregation process:
//  1. Collects all tools from registered backend servers via the registry
//  2. Collects all core tools from muster component providers
//  3. Combines both lists into a unified tool inventory
//  4. Returns tool names (with appropriate prefixing applied)
//
// This method is used by:
//   - Workflow manager for populating available tool lists
//   - Service class manager for tool validation
//   - Administrative interfaces for tool discovery
//
// Returns a slice of tool names representing all available tools.
func (a *AggregatorServer) GetAvailableTools() []string {
	// Get tools from external servers via registry
	registryTools := a.registry.GetAllTools()

	// Get core tools by recreating them using the same logic as updateCapabilities
	coreTools := a.createToolsFromProviders()

	// Combine all tool names from both sources
	allToolNames := make([]string, 0, len(registryTools)+len(coreTools))

	// Add tool names from registered backend servers
	for _, tool := range registryTools {
		allToolNames = append(allToolNames, tool.Name)
	}

	// Add tool names from core muster components
	for _, tool := range coreTools {
		allToolNames = append(allToolNames, tool.Tool.Name)
	}

	return allToolNames
}

// UpdateCapabilities provides public access to capability updates for external components.
//
// This method exposes the internal updateCapabilities functionality to allow
// other muster components (particularly the workflow manager) to trigger
// capability refreshes when they detect changes in their tool inventory.
//
// The method is thread-safe and can be called concurrently without causing
// issues. It performs the same comprehensive capability update as the internal
// method, including cleanup of obsolete items and addition of new capabilities.
//
// Use Cases:
//   - Workflow manager triggering updates when workflow definitions change
//   - Administrative tools forcing capability refresh
//   - Integration testing scenarios requiring capability synchronization
//
// This is a lightweight wrapper around the internal updateCapabilities method.
func (a *AggregatorServer) UpdateCapabilities() {
	a.updateCapabilities()
}

// OnToolsUpdated implements the ToolUpdateSubscriber interface for handling tool change events.
//
// This method responds to tool update events from other muster components,
// particularly the workflow manager, to maintain synchronization of the
// aggregator's exposed capabilities with the current tool landscape.
//
// Event Processing:
//   - Filters events to focus on workflow-related tool changes
//   - Triggers capability refresh for workflow tool updates
//   - Uses asynchronous processing with a small delay to avoid mutex conflicts
//   - Logs all received events for debugging and monitoring
//
// The asynchronous processing pattern ensures that:
//   - The event publisher (workflow manager) doesn't block waiting for aggregator updates
//   - Mutex conflicts are avoided by allowing the publisher to complete first
//   - Capability updates happen promptly but safely
//
// Args:
//   - event: Tool update event containing change information, tool lists, and metadata
//
// The method processes events selectively, focusing on workflow manager events
// that indicate changes to workflow-based tools.
func (a *AggregatorServer) OnToolsUpdated(event api.ToolUpdateEvent) {
	logging.InfoWithAttrs("Aggregator", "Received tool update event",
		slog.String("type", event.Type),
		slog.String("server", event.ServerName),
		slog.Int("tools", len(event.Tools)))

	// Handle workflow tool updates by refreshing capabilities
	if event.ServerName == "workflow-manager" && strings.HasPrefix(event.Type, "workflow_") {
		logging.InfoWithAttrs("Aggregator", "Refreshing capabilities due to workflow tool update",
			slog.String("type", event.Type))
		// Execute asynchronously to avoid blocking the event publisher and to ensure
		// the publisher has completed its operation before we query it for tools.
		// The goroutine scheduling provides the necessary separation without explicit delays.
		go a.updateCapabilities()
	}
}

// tryConnectWithToken attempts to establish a connection to an MCP server using an OAuth token.
// On success, it upgrades the session connection and returns a success result.
// On failure, it returns an error that the caller can use to determine next steps.
//
// This method delegates to the shared establishConnection helper to avoid code duplication.
// The issuer and scope parameters are used to create a MusterTokenStore that provides
// automatic token refresh via mcp-go's built-in OAuth handler.
func (a *AggregatorServer) tryConnectWithToken(ctx context.Context, serverName, serverURL, issuer, scope, accessToken string) (*mcp.CallToolResult, error) {
	result, err := establishConnection(ctx, a, serverName, serverURL, issuer, scope, accessToken)
	if err != nil {
		return nil, err
	}

	if result.Client != nil && a.connPool != nil {
		sessionID := getSessionIDFromContext(ctx)
		if sessionID != "" {
			a.connPool.Put(sessionID, serverName, result.Client)
		} else {
			logging.WarnWithAttrs("Aggregator", "Cannot pool client: no session ID in context",
				slog.String("server", serverName))
		}
	}

	return result.FormatAsMCPResult(), nil
}

// ProtectedResourceMetadata contains OAuth information discovered from
// the /.well-known/oauth-protected-resource endpoint.
type ProtectedResourceMetadata struct {
	// Issuer is the authorization server URL
	Issuer string
	// Scope is the space-separated list of required scopes
	Scope string
}

// discoverProtectedResourceMetadata fetches OAuth information from
// the server's /.well-known/oauth-protected-resource endpoint.
// This follows the MCP OAuth specification for resource metadata discovery (RFC 9728).
func discoverProtectedResourceMetadata(ctx context.Context, serverURL string) (*ProtectedResourceMetadata, error) {
	baseURL := pkgoauth.NormalizeServerURL(serverURL)
	resourceMetadataURL := baseURL + "/.well-known/oauth-protected-resource"

	req, err := http.NewRequestWithContext(ctx, "GET", resourceMetadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch resource metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resource metadata returned status %d", resp.StatusCode)
	}

	// Parse the response per RFC 9728
	var metadata struct {
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse resource metadata: %w", err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers in resource metadata")
	}

	result := &ProtectedResourceMetadata{
		Issuer: metadata.AuthorizationServers[0],
	}

	// Join all supported scopes with space separator
	if len(metadata.ScopesSupported) > 0 {
		result.Scope = strings.Join(metadata.ScopesSupported, " ")
	}

	return result, nil
}

// stdioDefaultUser is a placeholder session/subject key for non-OAuth transports.
//
// Most tool calls don't need it at all: non-session-auth servers use a global
// client on ServerInfo.Client and never touch the session-keyed stores.
//
// It only matters when a non-OAuth muster instance has auth-required DOWNSTREAM
// servers (e.g. core_auth_login to an OAuth-protected MCP server). That flow
// stores capabilities and connections in the session-keyed capability store and
// connection pool, which require some key. In production (OAuth-protected muster),
// the real token family ID from the bearer token is used instead.
//
// Injected explicitly into context at the transport layer (SetContextFunc for
// stdio, middleware wrapper for unauthenticated HTTP). Never used as a silent
// fallback — getSessionIDFromContext returns "" if nothing was injected.
const stdioDefaultUser = "default-user"

// getUserSubjectFromContext extracts the authenticated user's subject (sub claim)
// from the request context. Returns "" if no subject is available.
//
// Resolution order:
//  1. api.SubjectContextKey (set by createAccessTokenInjectorMiddleware)
//  2. oauth.UserInfo.ID (set by mcp-oauth ValidateToken — survives middleware early-exit)
//  3. "" (no subject — caller must handle)
func getUserSubjectFromContext(ctx context.Context) string {
	if sub := api.GetSubjectFromContext(ctx); sub != "" {
		return sub
	}
	if userInfo, ok := oauth.UserInfoFromContext(ctx); ok && userInfo != nil && userInfo.ID != "" {
		return userInfo.ID
	}
	return ""
}

// getTransportSessionID extracts the MCP transport session ID from the context.
// Returns "" if no MCP session is available.
func getTransportSessionID(ctx context.Context) string {
	if session := mcpserver.ClientSessionFromContext(ctx); session != nil {
		return session.SessionID()
	}
	return ""
}

// getSessionIDFromContext extracts the session ID (token family ID) from the
// request context. Returns "" if no session is available.
//
// Resolution order:
//  1. api.sessionIDContextKey (set by createAccessTokenInjectorMiddleware)
//  2. oauth.sessionIDKey (set by mcp-oauth ValidateToken — survives middleware early-exit)
//  3. "" (no session — caller must handle)
func getSessionIDFromContext(ctx context.Context) string {
	if sessionID := api.GetSessionIDFromContext(ctx); sessionID != "" {
		return sessionID
	}
	if sessionID, ok := oauth.SessionIDFromContext(ctx); ok {
		return sessionID
	}
	return ""
}

// tearDownSession clears all per-session server state: auth store entries,
// capability cache, pooled connections, and the subject tracker mapping.
// The oauth token store is NOT touched here — callers do that separately
// because the scope differs (per-user vs per-session).
func (a *AggregatorServer) tearDownSession(ctx context.Context, sessionID string) {
	if a.authStore != nil {
		if err := a.authStore.RevokeSession(ctx, sessionID); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to revoke auth session",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("error", err.Error()))
		}
	}
	if a.capabilityStore != nil {
		if err := a.capabilityStore.Delete(ctx, sessionID); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to delete session from capability store",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("error", err.Error()))
		}
	}
	if a.connPool != nil {
		a.connPool.EvictSession(sessionID)
	}
	if a.subjectSessions != nil {
		a.subjectSessions.UntrackOAuth(sessionID)
	}
}

// tearDownSessionServer clears the per-(session, server) state: oauth token
// for the server's issuer, auth store entry, capability entry, and the
// pooled connection.
func (a *AggregatorServer) tearDownSessionServer(ctx context.Context, sessionID string, serverInfo *ServerInfo) {
	if serverInfo.AuthInfo != nil && serverInfo.AuthInfo.Issuer != "" {
		if oauthHandler := api.GetOAuthHandler(); oauthHandler != nil && oauthHandler.IsEnabled() {
			oauthHandler.ClearTokenByIssuer(sessionID, serverInfo.AuthInfo.Issuer)
		}
	}
	serverName := serverInfo.Name
	if a.authStore != nil {
		if err := a.authStore.Revoke(ctx, sessionID, serverName); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to revoke auth",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("server", serverName),
				slog.String("error", err.Error()))
		}
	}
	if a.capabilityStore != nil {
		if err := a.capabilityStore.DeleteEntry(ctx, sessionID, serverName); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to delete entry from capability store",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("server", serverName),
				slog.String("error", err.Error()))
		}
	}
	if a.connPool != nil {
		a.connPool.Evict(sessionID, serverName)
	}
}

// handleUserTokensDeletion handles DELETE /user-tokens for "sign out everywhere".
// It clears all downstream tokens for the authenticated user across all sessions
// and invalidates all capability cache entries. This endpoint requires a valid
// Bearer token (ValidateToken middleware).
//
// Responses:
//   - 204 No Content: All downstream tokens cleared
//   - 401 Unauthorized: Missing or invalid Bearer token / subject
func (a *AggregatorServer) handleUserTokensDeletion(w http.ResponseWriter, r *http.Request) {
	sub := api.GetSubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "Unauthorized: missing subject", http.StatusUnauthorized)
		return
	}

	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() {
		oauthHandler.DeleteTokensByUser(sub)
	}

	if a.subjectSessions != nil {
		for _, sid := range a.subjectSessions.GetSessionIDs(sub) {
			a.tearDownSession(context.Background(), sid)
		}
	}

	logging.InfoWithAttrs("Aggregator", "All downstream tokens deleted for user via DELETE /user-tokens",
		slog.String("subject", logging.TruncateIdentifier(sub)))
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthServerDeletion handles DELETE /auth/{server} for per-server disconnect.
// It clears the downstream token for a specific server, closes the MCP client connection,
// and invalidates the cache entry. This is the HTTP equivalent of the core_auth_logout tool.
//
// Responses:
//   - 204 No Content: Server disconnected and token cleared
//   - 401 Unauthorized: Missing or invalid Bearer token / subject
//   - 404 Not Found: Server not found in registry
func (a *AggregatorServer) handleAuthServerDeletion(w http.ResponseWriter, r *http.Request) {
	sub := api.GetSubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "Unauthorized: missing subject", http.StatusUnauthorized)
		return
	}

	sessionID := getSessionIDFromContext(r.Context())
	if sessionID == "" {
		http.Error(w, "Unauthorized: missing session", http.StatusUnauthorized)
		return
	}

	serverName := r.PathValue("server")
	if serverName == "" {
		http.Error(w, "Bad Request: missing server name", http.StatusBadRequest)
		return
	}

	serverInfo, exists := a.registry.GetServerInfo(serverName)
	if !exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	a.tearDownSessionServer(context.Background(), sessionID, serverInfo)

	logging.InfoWithAttrs("Aggregator", "Server disconnected for session via DELETE /auth/{server}",
		slog.String("server", serverName),
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
	w.WriteHeader(http.StatusNoContent)
}

// resolveUserTool attempts to resolve a tool name through the CapabilityStore.
// This is used for OAuth-protected servers where tools are cached per-session.
//
// Returns the server name and original tool name, or an error if not found.
// Callers create an on-demand client via getOrCreateClientForToolCall.
func (a *AggregatorServer) resolveUserTool(sessionID, exposedName string) (string, string, error) {
	if a.capabilityStore == nil {
		return "", "", fmt.Errorf("capability store not initialized")
	}

	servers := a.registry.GetAllServers()
	for serverName, info := range servers {
		if !info.RequiresSessionAuth() {
			continue
		}

		caps, err := a.capabilityStore.Get(context.Background(), sessionID, serverName)
		if err != nil || caps == nil {
			continue
		}

		for _, tool := range caps.Tools {
			exposedToolName := a.registry.ExposedToolName(serverName, tool.Name)
			if exposedToolName == exposedName {
				return serverName, tool.Name, nil
			}
		}
	}

	return "", "", fmt.Errorf("tool not found in capability store")
}

// exchangeTokenAndCreateClient performs a token exchange for the given server
// and returns an uninitialized MCP client together with the exchanged token's
// expiry time. The caller is responsible for initializing and pooling the
// client.
//
// This is the single implementation of the token exchange + client creation
// flow, used by both getOrCreateClientForToolCall (synchronous path) and
// backgroundTokenRefresh (async path).
func (a *AggregatorServer) exchangeTokenAndCreateClient(
	ctx context.Context,
	serverInfo *ServerInfo,
	sessionID string,
) (MCPClient, time.Time, string, error) {
	serverName := serverInfo.Name
	musterIssuer := a.getMusterIssuer()
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil, time.Time{}, "", fmt.Errorf("OAuth handler not available for token exchange to %s", serverName)
	}

	idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer)
	if idToken == "" {
		return nil, time.Time{}, "", fmt.Errorf("no ID token available for token exchange to %s", serverName)
	}
	if isIDTokenExpired(idToken) {
		return nil, time.Time{}, "", fmt.Errorf("ID token has expired for %s, re-authenticate to refresh", serverName)
	}

	userID := extractUserIDFromToken(idToken)
	if userID == "" {
		return nil, time.Time{}, "", fmt.Errorf("failed to extract user ID from token for %s", serverName)
	}

	exchangeConfig := *serverInfo.AuthConfig.TokenExchange

	if exchangeConfig.ClientCredentialsSecretRef != nil {
		credentials, err := loadTokenExchangeCredentials(ctx, serverInfo)
		if err != nil {
			return nil, time.Time{}, "", fmt.Errorf("failed to load client credentials for %s: %w", serverName, err)
		}
		exchangeConfig.ClientID = credentials.ClientID
		exchangeConfig.ClientSecret = credentials.ClientSecret
	}

	if len(serverInfo.AuthConfig.RequiredAudiences) > 0 {
		updatedScopes, err := dex.AppendAudienceScopes(
			exchangeConfig.Scopes,
			serverInfo.AuthConfig.RequiredAudiences,
		)
		if err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to format audience scopes (continuing without audiences)",
				slog.String("server", serverName),
				slog.String("error", err.Error()))
		} else {
			exchangeConfig.Scopes = updatedScopes
		}
	}

	// Resolve the per-CR transport clients (TB-7/TB-8). For CRs without
	// spec.transport, mcpHTTPClient is a default http.Client and dexHTTPClient
	// is nil — equivalent to direct HTTPS.
	mcpHTTPClient, dexHTTPClient, err := a.resolveTransportClients(ctx, serverInfo)
	if err != nil {
		// Dispatcher error short-circuits the exchange; status condition is
		// already written by resolveTransportClients. TB-9: emit an audit
		// event for parity with connection_helper.go's failure path.
		emitTokenExchangeEvent(serverName, serverInfo.GetNamespace(), false, err.Error())
		return nil, time.Time{}, "", fmt.Errorf("resolve transport for %s: %w", serverName, err)
	}

	exchangedToken, err := oauthHandler.ExchangeTokenForRemoteClusterWithClient(
		ctx, idToken, userID, &exchangeConfig, dexHTTPClient,
	)
	if err != nil {
		// TB-9: emit a token-exchange audit event on failure for parity with
		// connection_helper.go.
		emitTokenExchangeEvent(serverName, serverInfo.GetNamespace(), false, err.Error())
		return nil, time.Time{}, "", fmt.Errorf("token exchange failed for %s: %w", serverName, err)
	}

	// TB-9: emit success audit event mirroring connection_helper.go.
	emitTokenExchangeEvent(serverName, serverInfo.GetNamespace(), true, "")

	tokenExpiry := getTokenExpiryTime(exchangedToken)

	headerFunc := func(_ context.Context) map[string]string {
		return map[string]string{"Authorization": "Bearer " + exchangedToken}
	}

	client := MCPClient(internalmcp.NewStreamableHTTPClientWithHeaderFuncAndHTTPClient(serverInfo.URL, headerFunc, mcpHTTPClient))

	return client, tokenExpiry, exchangedToken, nil
}

// getOrCreateClientForToolCall returns a pooled or freshly created MCP client
// for tool execution against an OAuth-protected server.
//
// The lookup order is:
//  1. Connection pool hit -> return pooled client with no-op cleanup
//  2. Pool miss -> create client, initialize, pool it, return with no-op cleanup
//
// The auth method is determined from ServerInfo.AuthConfig:
//   - Token exchange (RFC 8693): exchanges a fresh ID token for a server-specific token
//   - Token forwarding: forwards the user's ID token directly
//   - Standard OAuth (DynamicAuthClient): uses the token store via session ID
func (a *AggregatorServer) getOrCreateClientForToolCall(
	ctx context.Context,
	serverName string,
	sessionID string,
	sub string,
) (MCPClient, func(), error) {
	serverInfo, exists := a.registry.GetServerInfo(serverName)
	if !exists {
		return nil, nil, fmt.Errorf("server %s not found in registry", serverName)
	}

	if a.authStore == nil {
		return nil, nil, fmt.Errorf("auth store not initialized")
	}
	authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverName)
	if !authenticated {
		return nil, nil, fmt.Errorf("user not authenticated to server %s", serverName)
	}

	// Check the connection pool first.
	if a.connPool != nil {
		if pooledClient, ok := a.connPool.Get(sessionID, serverName); ok {
			if !ShouldUseTokenExchange(serverInfo) {
				logging.DebugWithAttrs("Aggregator", "Pool hit",
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
					slog.String("server", serverName))
				return pooledClient, func() {}, nil
			}

			tokenExpired := a.connPool.IsTokenExpired(sessionID, serverName)
			tokenExpiringSoon := a.connPool.IsTokenExpiringSoon(sessionID, serverName, tokenExchangeRefreshMargin)

			switch {
			case tokenExpired:
				// Token is dead -- must renew synchronously.
				logging.InfoWithAttrs("Aggregator", "Token expired, synchronous re-exchange required",
					slog.String("server", serverName),
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
				a.connPool.Evict(sessionID, serverName)
				// Fall through to pool-miss path.

			case tokenExpiringSoon:
				// Token still valid but nearing expiry -- return it immediately
				// and refresh in the background via singleflight.
				logging.InfoWithAttrs("Aggregator", "Token expiring soon, triggering background refresh",
					slog.String("server", serverName),
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
				a.triggerBackgroundTokenRefresh(sessionID, serverName, sub)
				return pooledClient, func() {}, nil

			default:
				// Token is healthy.
				logging.DebugWithAttrs("Aggregator", "Pool hit",
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
					slog.String("server", serverName))
				return pooledClient, func() {}, nil
			}
		}
	}

	var client MCPClient
	var tokenExpiry time.Time
	var exchangedToken string

	if ShouldUseTokenExchange(serverInfo) {
		var err error
		client, tokenExpiry, exchangedToken, err = a.exchangeTokenAndCreateClient(ctx, serverInfo, sessionID)
		if err != nil {
			return nil, nil, err
		}

	} else if ShouldUseTokenForwarding(serverInfo) {
		musterIssuer := a.getMusterIssuer()
		idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer)
		if idToken == "" {
			return nil, nil, fmt.Errorf("no ID token available for forwarding to %s", serverName)
		}

		if isIDTokenExpired(idToken) {
			return nil, nil, fmt.Errorf("ID token has expired for %s, re-authenticate to refresh", serverName)
		}

		headerFunc := func(_ context.Context) map[string]string {
			latestToken := getIDTokenForForwarding(context.Background(), sessionID, musterIssuer)
			if latestToken == "" {
				latestToken = idToken
			}
			if latestToken == "" {
				return map[string]string{}
			}
			return map[string]string{"Authorization": "Bearer " + latestToken}
		}
		client = internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc)

	} else if serverInfo.AuthInfo != nil && serverInfo.AuthInfo.Issuer != "" {
		oauthHandler := api.GetOAuthHandler()
		if oauthHandler == nil || !oauthHandler.IsEnabled() {
			return nil, nil, fmt.Errorf("OAuth handler not available for %s", serverName)
		}

		issuer := serverInfo.AuthInfo.Issuer
		scope := serverInfo.AuthInfo.Scope
		tokenStore := internalmcp.NewMusterTokenStore(sessionID, sub, issuer, oauthHandler)
		client = internalmcp.NewDynamicAuthClient(serverInfo.URL, tokenStore, scope)

	} else {
		return nil, nil, fmt.Errorf("unable to determine auth method for server %s", serverName)
	}

	// Initialize the on-demand client
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("failed to initialize on-demand client for %s: %w", serverName, err)
	}

	// Pool the client for future reuse. For token-exchange clients,
	// record the token expiry to enable proactive refresh.
	if a.connPool != nil {
		a.connPool.PutWithExpiry(sessionID, serverName, client, tokenExpiry)
		if exchangedToken != "" {
			a.connPool.SetExchangedToken(sessionID, serverName, exchangedToken)
		}
		logging.DebugWithAttrs("Aggregator", "Pooled new client",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("server", serverName))
	}

	return client, func() {}, nil
}

// callToolWithTokenExchangeRetry calls a tool on a session-scoped client,
// retrying once if the server returns 401 and uses token exchange.
//
// Token exchange produces a fixed-lifetime token baked into the client's
// headerFunc. If the token expires while the client is pooled, the downstream
// server returns 401. This method detects that case, evicts the stale pool
// entry, creates a fresh client (which re-exchanges the token), and retries.
//
// Token forwarding clients do not need this retry because their headerFunc
// dynamically resolves the latest token on each request.
func (a *AggregatorServer) callToolWithTokenExchangeRetry(
	ctx context.Context,
	serverName string,
	originalToolName string,
	args map[string]interface{},
	sessionID string,
	sub string,
) (*mcp.CallToolResult, error) {
	client, cleanup, err := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, sub)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server %s: %w", serverName, err)
	}
	defer cleanup()

	result, callErr := client.CallTool(ctx, originalToolName, args)
	if callErr == nil {
		return result, nil
	}

	serverInfo, exists := a.registry.GetServerInfo(serverName)
	if !exists || !ShouldUseTokenExchange(serverInfo) || !is401Error(callErr) {
		return nil, callErr
	}

	logging.InfoWithAttrs("Aggregator", "Token expired for token-exchange server, evicting pooled client and re-exchanging",
		slog.String("server", serverName),
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))

	if a.connPool != nil {
		a.connPool.Evict(sessionID, serverName)
	}

	retryClient, retryCleanup, retryErr := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, sub)
	if retryErr != nil {
		return nil, fmt.Errorf("retry after token re-exchange failed for %s: %w", serverName, retryErr)
	}
	defer retryCleanup()

	return retryClient.CallTool(ctx, originalToolName, args)
}

// triggerBackgroundTokenRefresh launches a background goroutine to re-exchange
// the token for a token-exchange server and replace the pooled client.
// Concurrent calls for the same (sessionID, serverName) are deduplicated via
// singleflight -- only the first caller spawns the goroutine.
func (a *AggregatorServer) triggerBackgroundTokenRefresh(sessionID, serverName, sub string) {
	sfKey := sessionID + "/" + serverName
	go func() {
		_, _, _ = a.tokenRefreshGroup.Do(sfKey, func() (interface{}, error) {
			a.backgroundTokenRefresh(sessionID, serverName, sub)
			return nil, nil
		})
	}()
}

// backgroundTokenRefresh performs a token re-exchange and replaces the pooled
// client with a freshly authenticated one. The old client is closed after a
// delay (deferredCloseDelay) to let any in-flight requests complete.
//
// Failures are logged but never propagated -- the caller already received the
// still-valid pooled client, and callToolWithTokenExchangeRetry handles the
// case where the token expires before the background refresh succeeds.
func (a *AggregatorServer) backgroundTokenRefresh(sessionID, serverName, sub string) {
	ctx := context.Background()

	serverInfo, exists := a.registry.GetServerInfo(serverName)
	if !exists || !ShouldUseTokenExchange(serverInfo) {
		return
	}

	client, tokenExpiry, exchangedToken, err := a.exchangeTokenAndCreateClient(ctx, serverInfo, sessionID)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Background token refresh failed",
			slog.String("server", serverName),
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("error", err.Error()))
		return
	}

	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		logging.WarnWithAttrs("Aggregator", "Background refresh: client init failed",
			slog.String("server", serverName),
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("error", err.Error()))
		return
	}

	if a.connPool != nil {
		a.connPool.PutWithDeferredClose(sessionID, serverName, client, tokenExpiry, deferredCloseDelay)
		if exchangedToken != "" {
			a.connPool.SetExchangedToken(sessionID, serverName, exchangedToken)
		}
	}

	logging.InfoWithAttrs("Aggregator", "Background token refresh completed",
		slog.String("server", serverName),
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
}

// ============================================================================
// MetaToolsDataProvider interface implementation
// ============================================================================
// The following methods implement the api.MetaToolsDataProvider interface,
// enabling the metatools package to access tools, resources, and prompts
// through the aggregator.

// ListToolsForContext returns all available tools for the current user context.
// This is used by the metatools package to provide user-scoped tool visibility.
//
// The method extracts the session ID from the context and returns tools
// appropriate for that session's authentication state. This includes:
//   - MCP server tools (prefixed with x_<server>_)
//   - Core muster tools (prefixed with core_) from internal providers
//
// The core tools are collected from workflow, service, config, serviceclass,
// mcpserver, events, and auth providers.
func (a *AggregatorServer) ListToolsForContext(ctx context.Context) []mcp.Tool {
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		logging.Warn("Aggregator", "ListToolsForContext: no session ID in context — returning core tools only")
		return a.getAllCoreToolsAsMCPTools()
	}

	mcpServerTools := a.GetToolsForSession(ctx, sessionID)
	coreTools := a.getAllCoreToolsAsMCPTools()

	allTools := make([]mcp.Tool, 0, len(mcpServerTools)+len(coreTools))
	allTools = append(allTools, mcpServerTools...)
	allTools = append(allTools, coreTools...)

	logging.DebugWithAttrs("Aggregator", "ListToolsForContext: returning tools",
		slog.Int("total", len(allTools)),
		slog.Int("mcpServer", len(mcpServerTools)),
		slog.Int("core", len(coreTools)),
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))

	return allTools
}

// ListResourcesForContext returns all available resources for the current session context.
func (a *AggregatorServer) ListResourcesForContext(ctx context.Context) []mcp.Resource {
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		logging.Warn("Aggregator", "ListResourcesForContext: no session ID in context — returning empty")
		return nil
	}
	return a.GetResourcesForSession(ctx, sessionID)
}

// ListPromptsForContext returns all available prompts for the current session context.
func (a *AggregatorServer) ListPromptsForContext(ctx context.Context) []mcp.Prompt {
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		logging.Warn("Aggregator", "ListPromptsForContext: no session ID in context — returning empty")
		return nil
	}
	return a.GetPromptsForSession(ctx, sessionID)
}

// ReadResource retrieves the contents of a resource by URI.
// This resolves the resource URI to its origin server and reads the content.
func (a *AggregatorServer) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	// Resolve the exposed URI back to server and original URI
	serverName, originalURI, err := a.registry.ResolveResourceName(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve resource URI: %w", err)
	}

	// Get the backend client
	client, err := a.registry.GetClient(serverName)
	if err != nil {
		return nil, fmt.Errorf("server not available: %w", err)
	}

	// Read the resource from the backend server
	result, err := client.ReadResource(ctx, originalURI)
	if err != nil {
		return nil, fmt.Errorf("resource read failed: %w", err)
	}

	return result, nil
}

// GetPrompt executes a prompt with the provided arguments.
// This resolves the prompt name to its origin server and retrieves the prompt.
func (a *AggregatorServer) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	// Resolve the exposed name back to server and original prompt name
	serverName, originalName, err := a.registry.ResolvePromptName(name)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve prompt name: %w", err)
	}

	// Get the backend client
	client, err := a.registry.GetClient(serverName)
	if err != nil {
		return nil, fmt.Errorf("server not available: %w", err)
	}

	// Convert string args to interface{} args for the client
	clientArgs := make(map[string]interface{})
	for k, v := range args {
		clientArgs[k] = v
	}

	// Get the prompt from the backend server
	result, err := client.GetPrompt(ctx, originalName, clientArgs)
	if err != nil {
		return nil, fmt.Errorf("prompt retrieval failed: %w", err)
	}

	return result, nil
}

// ListServersRequiringAuth returns a list of servers that require authentication
// for the current session. This enables the list_tools meta-tool to inform users
// about servers that are available but require authentication before their tools
// become visible.
//
// The method checks each registered server and returns those that:
//   - Require per-session authentication (RequiresSessionAuth)
//   - The session has not yet authenticated to
//   - Are NOT SSO-configured (token forwarding/exchange)
//
// This is part of the server-side meta-tools migration (Issue #343) to provide
// better visibility into which servers need authentication.
func (a *AggregatorServer) ListServersRequiringAuth(ctx context.Context) []api.ServerAuthInfo {
	sessionID := getSessionIDFromContext(ctx)
	servers := a.registry.GetAllServers()

	var authRequired []api.ServerAuthInfo

	for name, info := range servers {
		if !info.RequiresSessionAuth() {
			continue
		}

		// SSO-enabled servers (token forwarding/exchange) are authenticated by
		// the admin, not the user -- manual login cannot fix SSO failures.
		if ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info) {
			continue
		}

		if a.authStore != nil {
			authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, name)
			if authenticated {
				continue
			}
		}

		authRequired = append(authRequired, api.ServerAuthInfo{
			Name:     name,
			Status:   "auth_required",
			AuthTool: "core_auth_login",
		})
	}

	logging.DebugWithAttrs("Aggregator", "ListServersRequiringAuth",
		slog.Int("servers", len(authRequired)),
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))

	return authRequired
}
