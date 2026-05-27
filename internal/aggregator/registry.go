package aggregator

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	"github.com/giantswarm/muster/internal/metatools"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/runtime"
)

// resolvedName stores the reverse mapping from an exposed (prefixed) name
// back to its origin server and original name.
type resolvedName struct {
	serverName   string
	originalName string
	itemType     metatools.ItemKind
}

// familyBucket is the per-exposed-name routing record for a family-grouped
// tool. Keeping family + instanceArg alongside the providers lets
// ResolveToolName produce its error message and CallToolInternal strip the
// instance-selector arg without re-deriving the family name from the prefix.
type familyBucket struct {
	family      string
	instanceArg string
	providers   []resolvedName
}

// ServerRegistry manages the collection of registered MCP servers and their capabilities.
//
// The registry maintains a thread-safe mapping of server names to their information,
// including cached capabilities (tools, resources, prompts) and connection status.
// It applies deterministic prefixing ({musterPrefix}_{serverPrefix}_{originalName})
// and maintains a reverse lookup map for routing requests to the correct backend.
//
// Servers that declare spec.family share their exposed name space: every server
// in a family advertises tools as {musterPrefix}_{family}_{toolName} and the
// aggregator injects a required "server" enum parameter so callers select which
// instance handles the call. The "server" parameter is always required when
// family is set, even for single-instance families, so skills written against
// the family name remain stable when instances are added or removed.
//
// Key responsibilities:
//   - Server lifecycle management (registration/deregistration)
//   - Capability caching for performance
//   - Deterministic name prefixing and reverse resolution
//   - Family-based grouping with explicit instance routing
//   - Thread-safe access to server information
//   - Update notifications for capability changes
type ServerRegistry struct {
	servers map[string]*ServerInfo // Map of server name to server information
	mu      sync.RWMutex           // Protects concurrent access to servers map

	// Channel for notifying subscribers about registry changes
	updateChan chan struct{}

	// Reverse lookup: exposed name -> origin server + original name.
	// Populated as a side effect of ExposedToolName/ExposedPromptName/ExposedResourceURI.
	// Protected by nameMu (separate from mu to avoid deadlocks when
	// GetAllTools/GetAllResources/GetAllPrompts call ExposedXxx while holding mu.RLock).
	// serverPrefixes and serverFamilies are also protected by nameMu.
	nameMapping    map[string]resolvedName
	serverPrefixes map[string]string               // server name -> configured prefix
	serverFamilies map[string]*api.MCPServerFamily // server name -> declared family (nil if none)
	nameMu         sync.RWMutex
	musterPrefix   string

	// familyMappings indexes every exposed family tool name back to the family
	// name, required instance arg, and set of providing servers. An entry
	// exists only for tools that are family-grouped; single-server tools fall
	// through to nameMapping. Keys are exposed names (e.g.
	// x_kubernetes_list_pods). Protected by nameMu.
	familyMappings map[string]*familyBucket
}

// NewServerRegistry creates a new server registry with the specified global prefix.
//
// The registry uses the musterPrefix to ensure all exposed capabilities are
// prefixed appropriately to distinguish them from other MCP tools in the environment.
//
// Args:
//   - musterPrefix: Global prefix applied to all aggregated capabilities (default: "x")
//
// Returns a new, empty server registry ready for use.
func NewServerRegistry(musterPrefix string) *ServerRegistry {
	if musterPrefix == "" {
		musterPrefix = "x"
	}
	return &ServerRegistry{
		servers:        make(map[string]*ServerInfo),
		updateChan:     make(chan struct{}, 1),
		nameMapping:    make(map[string]resolvedName),
		serverPrefixes: make(map[string]string),
		serverFamilies: make(map[string]*api.MCPServerFamily),
		musterPrefix:   musterPrefix,
		familyMappings: make(map[string]*familyBucket),
	}
}

// ExposedToolName returns the fully prefixed name for a tool and records
// the reverse mapping for later resolution.
//
// Pattern: {musterPrefix}_{serverPrefix}_{originalName}
func (r *ServerRegistry) ExposedToolName(serverName, toolName string) string {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	exposed := r.buildExposedNameLocked(serverName, toolName)
	r.nameMapping[exposed] = resolvedName{serverName: serverName, originalName: toolName, itemType: metatools.ItemKindTool}
	return exposed
}

// ExposedPromptName returns the fully prefixed name for a prompt and records
// the reverse mapping for later resolution.
func (r *ServerRegistry) ExposedPromptName(serverName, promptName string) string {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	exposed := r.buildExposedNameLocked(serverName, promptName)
	r.nameMapping[exposed] = resolvedName{serverName: serverName, originalName: promptName, itemType: metatools.ItemKindPrompt}
	return exposed
}

// ExposedResourceURI returns the fully prefixed URI for a resource and records
// the reverse mapping for later resolution. URIs with a scheme (e.g. http://)
// are returned unchanged.
func (r *ServerRegistry) ExposedResourceURI(serverName, resourceURI string) string {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	if strings.Contains(resourceURI, "://") {
		r.nameMapping[resourceURI] = resolvedName{serverName: serverName, originalName: resourceURI, itemType: metatools.ItemKindResource}
		return resourceURI
	}
	exposed := r.buildExposedNameLocked(serverName, resourceURI)
	r.nameMapping[exposed] = resolvedName{serverName: serverName, originalName: resourceURI, itemType: metatools.ItemKindResource}
	return exposed
}

// buildExposedNameLocked constructs {musterPrefix}_{serverPrefix}_{name}.
// Caller must hold nameMu.
func (r *ServerRegistry) buildExposedNameLocked(serverName, name string) string {
	prefix := r.serverPrefixes[serverName]
	if prefix == "" {
		prefix = serverName
	}
	if !strings.HasPrefix(name, prefix+"_") {
		name = prefix + "_" + name
	}
	return r.musterPrefix + "_" + name
}

// SetServerPrefix configures the prefix to use for a specific server.
// If prefix is empty the server name itself is used.
func (r *ServerRegistry) SetServerPrefix(serverName, prefix string) {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	r.setServerPrefixLocked(serverName, prefix)
}

// setServerPrefixLocked sets the prefix for a server. Caller must hold nameMu.
func (r *ServerRegistry) setServerPrefixLocked(serverName, prefix string) {
	if prefix == "" {
		prefix = serverName
	}
	r.serverPrefixes[serverName] = prefix
}

// setServerFamilyLocked records the family declared by a server (or removes
// the entry when family is nil). Caller must hold nameMu.
func (r *ServerRegistry) setServerFamilyLocked(serverName string, family *api.MCPServerFamily) {
	if family == nil || family.Name == "" {
		delete(r.serverFamilies, serverName)
		return
	}
	clone := *family
	r.serverFamilies[serverName] = &clone
}

// familyExposedName returns the family-scoped exposed name for a tool:
// {musterPrefix}_{family}_{toolName}. If the original tool name already
// carries the family prefix it is not duplicated.
func (r *ServerRegistry) familyExposedName(family, toolName string) string {
	if !strings.HasPrefix(toolName, family+"_") {
		toolName = family + "_" + toolName
	}
	return r.musterPrefix + "_" + toolName
}

// Register adds a new MCP server to the registry and initializes its capabilities.
//
// This method performs the following operations:
//  1. Validates that the server name is not already in use
//  2. Initializes the MCP client if needed
//  3. Queries the server for its initial capabilities
//  4. Stores the server information and updates the name tracker
//  5. Notifies subscribers of the registry update
//
// The method is thread-safe and can be called concurrently.
//
// Args:
//   - ctx: Context for initialization and capability queries
//   - registration: Server identification (name, toolPrefix, family)
//   - client: MCP client instance for communicating with the server
//
// Returns an error if the server name is already registered, client initialization
// fails, or the server cannot be reached.
func (r *ServerRegistry) Register(ctx context.Context, registration ServerRegistration, client MCPClient) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[registration.Name]; exists {
		return fmt.Errorf("server %s already registered", registration.Name)
	}

	// Check if client is already initialized, if not try to initialize
	if initializer, ok := client.(interface{ Initialize(context.Context) error }); ok {
		// Use a short timeout to avoid blocking the registration process
		initCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		if err := initializer.Initialize(initCtx); err != nil {
			return fmt.Errorf("failed to initialize client for %s: %w", registration.Name, err)
		}
	}

	info := &ServerInfo{
		Name:       registration.Name,
		Client:     client,
		ToolPrefix: registration.ToolPrefix,
		Family:     cloneFamily(registration.Family),
	}

	r.applyServerRegistrationLocked(registration.Name, registration.ToolPrefix, registration.Family)

	// Fetch initial capabilities from the server
	if err := r.refreshServerCapabilities(ctx, info); err != nil {
		logging.Warn("Aggregator", "Failed to get initial capabilities for %s: %v", registration.Name, err)
		// Log diagnostic information about partial success
		info.mu.RLock()
		logging.Debug("Aggregator", "Server %s registered with %d tools, %d resources, %d prompts",
			registration.Name, len(info.Tools), len(info.Resources), len(info.Prompts))
		info.mu.RUnlock()
	} else {
		info.mu.RLock()
		logging.Info("Aggregator", "Server %s registered successfully with %d tools, %d resources, %d prompts",
			registration.Name, len(info.Tools), len(info.Resources), len(info.Prompts))
		info.mu.RUnlock()
	}

	r.servers[registration.Name] = info
	r.notifyUpdate()

	logging.Info("Aggregator", "Registered MCP server: %s", registration.Name)
	return nil
}

// Deregister removes an MCP server from the registry and cleans up its resources.
//
// This method safely removes a server from the registry, closes its client connection,
// and notifies subscribers of the change. All tools, resources, and prompts provided
// by the server will no longer be available through the aggregator.
//
// The method is thread-safe and can be called concurrently.
//
// Args:
//   - name: Unique identifier of the server to remove
//
// Returns an error if the server is not found in the registry.
func (r *ServerRegistry) Deregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, exists := r.servers[name]
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	if info.Client != nil {
		if err := info.Client.Close(); err != nil {
			logging.Warn("Aggregator", "Error closing client for %s: %v", name, err)
		}
	}

	delete(r.servers, name)

	// Drop family routing state owned by this server so that subsequent calls
	// to a family-grouped tool can no longer be routed to a deregistered
	// instance. Entries that still have surviving providers keep them;
	// entries with no surviving providers are removed entirely. Solo
	// nameMapping entries are intentionally left in place — callers that hit
	// them get a "server not found" error from the dispatch layer, which is
	// the long-standing semantic for "the server providing this tool is
	// gone".
	r.nameMu.Lock()
	delete(r.serverFamilies, name)
	for exposed, bucket := range r.familyMappings {
		filtered := bucket.providers[:0]
		for _, p := range bucket.providers {
			if p.serverName != name {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			delete(r.familyMappings, exposed)
			continue
		}
		bucket.providers = filtered
	}
	r.nameMu.Unlock()

	r.notifyUpdate()

	logging.Info("Aggregator", "Deregistered MCP server: %s", name)
	return nil
}

// GetClient returns the MCP client for a specific registered server.
//
// This method provides access to the underlying MCP client for direct communication
// with a specific server. The client can be used to execute tools, read resources,
// or retrieve prompts from the server.
//
// Args:
//   - name: Unique identifier of the server
//
// Returns the MCP client interface and nil error if successful.
// Returns nil client and an error if the server is not found or not connected.
func (r *ServerRegistry) GetClient(name string) (MCPClient, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.servers[name]
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
	}

	if !info.IsConnected() {
		return nil, fmt.Errorf("server %s is not connected", name)
	}

	return info.Client, nil
}

// GetAllTools returns a consolidated list of all tools from all connected servers.
//
// Tools from servers that share a non-empty spec.family are grouped under a
// single exposed name ({musterPrefix}_{family}_{toolName}) with a required
// "server" enum parameter selecting the providing instance. Tools from
// servers without a family fall back to per-server prefixing
// ({musterPrefix}_{serverPrefix}_{originalName}).
//
// Per ADR-008, servers in auth_required state do NOT contribute any tools.
// Users must use core_auth_login to authenticate before server tools become visible.
//
// Returns a slice of MCP tools ready for exposure through the aggregator.
func (r *ServerRegistry) GetAllTools() []mcp.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var contributions []serverToolContribution
	connectedCount := 0
	authRequiredCount := 0

	for serverName, info := range r.servers {
		// Per ADR-008: Servers requiring authentication do NOT expose any tools
		// Users must use core_auth_login to authenticate first
		if info.RequiresSessionAuth() {
			authRequiredCount++
			logging.Debug("Aggregator", "Server %s requires auth, no tools exposed (use core_auth_login)", serverName)
			continue
		}

		if !info.IsConnected() {
			logging.Debug("Aggregator", "Server %s is not connected, skipping tools", serverName)
			continue
		}
		connectedCount++

		info.mu.RLock()
		toolsCopy := append([]mcp.Tool(nil), info.Tools...)
		info.mu.RUnlock()

		contributions = append(contributions, serverToolContribution{
			serverName: serverName,
			family:     cloneFamily(info.Family),
			tools:      toolsCopy,
		})
		logging.Debug("Aggregator", "Server %s has %d tools", serverName, len(toolsCopy))
	}

	allTools := r.assembleExposedTools(contributions)
	logging.Debug("Aggregator", "GetAllTools: returning %d tools from %d connected servers (%d servers require auth, use core_auth_login)",
		len(allTools), connectedCount, authRequiredCount)

	return allTools
}

// serverToolContribution carries one server's tool contribution into the
// family-aware assembly pipeline used by GetAllTools / GetAllToolsForSession.
type serverToolContribution struct {
	serverName string
	family     *api.MCPServerFamily
	tools      []mcp.Tool
}

// assembleExposedTools turns a set of per-server tool contributions into the
// flat exposed list, applying family grouping. It upserts the
// familyMappings index by union so per-session contributions add providers
// without overwriting other sessions' visibility. Deregister is the only
// path that removes providers from the index.
//
// Family grouping rules:
//   - Servers without a family always emit per-server prefixed tools.
//   - Family-level invariants are checked first against the global
//     serverFamilies state: any family whose members declare different
//     family.instanceArg falls back ENTIRELY to per-server prefixing for
//     all its tools — independent of whether their tool sets overlap.
//   - Per-tool divergence within a non-fallback family (diverging
//     descriptions) falls back only that specific tool to per-server
//     prefixing; other tools in the family still group.
//   - Tools in a non-fallback family emit a single exposed tool per
//     original tool name with a required instance-selector enum.
func (r *ServerRegistry) assembleExposedTools(contributions []serverToolContribution) []mcp.Tool {
	r.nameMu.RLock()
	familyFallback := r.familyFallbackStatusLocked()
	r.nameMu.RUnlock()

	// Sort contributions by serverName so emission order is independent of
	// the map iteration order in GetAllTools / GetAllToolsForSession.
	// Without this, two successive tools/list calls can return the same set
	// of tools in different orders, surfacing as spurious tools/list_changed
	// diffs and flaky downstream assertions.
	slices.SortFunc(contributions, func(a, b serverToolContribution) int {
		return strings.Compare(a.serverName, b.serverName)
	})

	type familyKey struct {
		family   string
		toolName string
	}
	type familyEntry struct {
		servers     []string
		tools       []mcp.Tool // parallel to servers
		description string
		descMatches bool
		instanceArg string
	}

	familyBuckets := make(map[familyKey]*familyEntry)
	var soloTools []mcp.Tool

	for _, c := range contributions {
		inFallbackFamily := c.family != nil && c.family.Name != "" && familyFallback[c.family.Name]
		if c.family == nil || c.family.Name == "" || inFallbackFamily {
			for _, tool := range c.tools {
				exposedTool := tool
				exposedTool.Name = r.ExposedToolName(c.serverName, tool.Name)
				soloTools = append(soloTools, exposedTool)
			}
			continue
		}
		for _, tool := range c.tools {
			key := familyKey{family: c.family.Name, toolName: tool.Name}
			entry, ok := familyBuckets[key]
			if !ok {
				familyBuckets[key] = &familyEntry{
					servers:     []string{c.serverName},
					tools:       []mcp.Tool{tool},
					description: tool.Description,
					descMatches: true,
					instanceArg: c.family.InstanceArg,
				}
				continue
			}
			entry.servers = append(entry.servers, c.serverName)
			entry.tools = append(entry.tools, tool)
			if tool.Description != entry.description {
				entry.descMatches = false
			}
		}
	}

	// Sort family bucket keys so family-grouped tools are emitted in a
	// stable order across calls. familyBuckets is a Go map and its native
	// iteration order is randomized.
	familyKeys := make([]familyKey, 0, len(familyBuckets))
	for k := range familyBuckets {
		familyKeys = append(familyKeys, k)
	}
	slices.SortFunc(familyKeys, func(a, b familyKey) int {
		if c := strings.Compare(a.family, b.family); c != 0 {
			return c
		}
		return strings.Compare(a.toolName, b.toolName)
	})

	for _, key := range familyKeys {
		entry := familyBuckets[key]
		if !entry.descMatches {
			logging.Warn("Aggregator",
				"family %q tool %q has divergent descriptions across servers %v; falling back to per-server prefixing for this tool",
				key.family, key.toolName, entry.servers)
			soloTools = append(soloTools, r.perServerTools(entry.servers, entry.tools)...)
			r.removeFamilyTool(r.familyExposedName(key.family, key.toolName))
			continue
		}
		if instanceArgCollides(entry.tools, entry.instanceArg) {
			logging.Warn("Aggregator",
				"family %q tool %q already declares an input property named %q on at least one contributor; family.instanceArg collides — falling back to per-server prefixing for this tool",
				key.family, key.toolName, entry.instanceArg)
			soloTools = append(soloTools, r.perServerTools(entry.servers, entry.tools)...)
			r.removeFamilyTool(r.familyExposedName(key.family, key.toolName))
			continue
		}

		sortedServers := append([]string(nil), entry.servers...)
		sort.Strings(sortedServers)

		exposedTool := entry.tools[0]
		exposedTool.Name = r.familyExposedName(key.family, key.toolName)
		exposedTool.InputSchema = injectInstanceEnum(exposedTool.InputSchema, entry.instanceArg, sortedServers)
		exposedTool.Description = annotateMultiServer(exposedTool.Description, sortedServers)

		soloTools = append(soloTools, exposedTool)
		r.upsertFamilyTool(exposedTool.Name, key.toolName, key.family, entry.instanceArg, sortedServers)
	}

	return soloTools
}

// applyServerRegistrationLocked records the server's prefix and declared
// family in the registry's name tables, and — if adding this server tips
// its family into instanceArg divergence — purges any stale family-grouped
// routing entries and emits a single state-change warning. Used by both
// Register and RegisterPendingAuth so the family-conflict surface is
// identical for stdio and OAuth servers. Caller must NOT hold nameMu.
func (r *ServerRegistry) applyServerRegistrationLocked(name, toolPrefix string, family *api.MCPServerFamily) {
	r.nameMu.Lock()
	r.setServerPrefixLocked(name, toolPrefix)
	r.setServerFamilyLocked(name, family)
	var (
		fallbackTriggered bool
		fallbackMembers   []string
	)
	if family != nil && family.Name != "" {
		if r.familyFallbackStatusLocked()[family.Name] {
			r.purgeFamilyFromIndexLocked(family.Name)
			fallbackTriggered = true
			fallbackMembers = r.familyMembersLocked(family.Name)
		}
	}
	r.nameMu.Unlock()
	if fallbackTriggered {
		logging.Warn("Aggregator",
			"family %q has divergent family.instanceArg across members %v; falling back to per-server prefixing for the entire family",
			family.Name, fallbackMembers)
	}
}

// familyFallbackStatusLocked returns the set of family names whose declared
// members disagree on family.instanceArg. Detection is global (driven by
// serverFamilies), so a family-level fallback fires even when no two members
// happen to share a tool name. Caller must hold nameMu (read or write).
//
// This function is pure — the divergence warning is emitted at state change
// in Register/RegisterPendingAuth, not on every tools/list call.
func (r *ServerRegistry) familyFallbackStatusLocked() map[string]bool {
	seenInstanceArg := make(map[string]string)
	fallback := make(map[string]bool)
	for _, f := range r.serverFamilies {
		if f == nil || f.Name == "" {
			continue
		}
		if existing, ok := seenInstanceArg[f.Name]; ok {
			if existing != f.InstanceArg {
				fallback[f.Name] = true
			}
			continue
		}
		seenInstanceArg[f.Name] = f.InstanceArg
	}
	return fallback
}

// familyMembersLocked returns the sorted list of server names declaring the
// given family.Name. Caller must hold nameMu.
func (r *ServerRegistry) familyMembersLocked(familyName string) []string {
	var members []string
	for serverName, f := range r.serverFamilies {
		if f != nil && f.Name == familyName {
			members = append(members, serverName)
		}
	}
	sort.Strings(members)
	return members
}

// upsertFamilyTool unions the given providers into the family routing
// index entry for exposedName, creating the entry if absent. Existing
// providers are preserved; only previously unseen serverNames are appended.
// This keeps per-session listings from overwriting each other's view of the
// family's full membership — Deregister remains the sole path that removes
// providers.
// Caller must NOT hold nameMu.
func (r *ServerRegistry) upsertFamilyTool(exposedName, originalName, family, instanceArg string, serverNames []string) {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	bucket, ok := r.familyMappings[exposedName]
	if !ok {
		providers := make([]resolvedName, len(serverNames))
		for i, sn := range serverNames {
			providers[i] = resolvedName{serverName: sn, originalName: originalName, itemType: metatools.ItemKindTool}
		}
		r.familyMappings[exposedName] = &familyBucket{
			family:      family,
			instanceArg: instanceArg,
			providers:   providers,
		}
		return
	}
	bucket.family = family
	bucket.instanceArg = instanceArg
	existing := make(map[string]struct{}, len(bucket.providers))
	for _, p := range bucket.providers {
		existing[p.serverName] = struct{}{}
	}
	for _, sn := range serverNames {
		if _, dup := existing[sn]; dup {
			continue
		}
		bucket.providers = append(bucket.providers, resolvedName{serverName: sn, originalName: originalName, itemType: metatools.ItemKindTool})
	}
}

// perServerTools returns the given tools renamed with per-server exposed
// prefixes (one per servers[i]/tools[i] pair). Used by the per-tool
// fallback branches in assembleExposedTools so divergence-of-any-kind
// handlers share one emission shape. Caller must NOT hold nameMu (because
// ExposedToolName acquires it).
func (r *ServerRegistry) perServerTools(servers []string, tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, len(servers))
	for i, srv := range servers {
		out[i] = tools[i]
		out[i].Name = r.ExposedToolName(srv, tools[i].Name)
	}
	return out
}

// purgeFamilyFromIndexLocked removes every family-grouped routing entry
// whose bucket belongs to the given family.Name. Used when a family
// transitions into fallback so stale family-prefixed names cannot keep
// routing through a no-longer-valid grouping. Caller must hold nameMu.
func (r *ServerRegistry) purgeFamilyFromIndexLocked(familyName string) {
	for exposed, bucket := range r.familyMappings {
		if bucket.family == familyName {
			delete(r.familyMappings, exposed)
		}
	}
}

// removeFamilyTool removes a single family-grouped routing entry by exposed
// name. Used when per-tool divergence (e.g. diverging descriptions) is
// detected and the previously-grouped exposed name must no longer route via
// the family bucket. Caller must NOT hold nameMu.
func (r *ServerRegistry) removeFamilyTool(exposedName string) {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	delete(r.familyMappings, exposedName)
}

// injectInstanceEnum returns a copy of schema with a required string
// parameter (named instanceArg) whose enum lists the available backend
// servers. Properties and Required are deep-copied — including nested
// object properties and array items — so the per-server cached tool schema
// is not mutated by callers that walk the returned schema.
func injectInstanceEnum(schema mcp.ToolInputSchema, instanceArg string, servers []string) mcp.ToolInputSchema {
	enumVals := make([]any, len(servers))
	for i, s := range servers {
		enumVals[i] = s
	}

	properties := runtime.DeepCopyJSON(schema.Properties)
	if properties == nil {
		properties = make(map[string]any, 1)
	}
	properties[instanceArg] = map[string]any{
		"type":        "string",
		"description": "Target instance to execute this tool on. Available: " + strings.Join(servers, ", "),
		"enum":        enumVals,
	}

	required := make([]string, 0, len(schema.Required)+1)
	required = append(required, schema.Required...)
	required = append(required, instanceArg)

	schema.Properties = properties
	schema.Required = required
	return schema
}

// instanceArgCollides reports whether instanceArg matches a property name on
// any contributing tool's InputSchema. The collision is asymmetric: server A's
// tool can lack the property while server B's tool declares it, in which case
// grouping would still silently overwrite B's declaration. Check every
// contributor, not just the first.
func instanceArgCollides(tools []mcp.Tool, instanceArg string) bool {
	if instanceArg == "" {
		return false
	}
	for _, t := range tools {
		if _, ok := t.InputSchema.Properties[instanceArg]; ok {
			return true
		}
	}
	return false
}

// cloneFamily returns a deep copy of the given family pointer, or nil.
func cloneFamily(f *api.MCPServerFamily) *api.MCPServerFamily {
	if f == nil {
		return nil
	}
	clone := *f
	return &clone
}

// annotateMultiServer appends a (available on servers: …) trailer to the
// description so MCP clients reading tools/list see which instances the
// "server" parameter accepts. A single trailer is preserved across repeated
// passes by treating an existing parenthesised "available on servers" suffix
// as canonical.
func annotateMultiServer(description string, servers []string) string {
	trailer := " (available on servers: " + strings.Join(servers, ", ") + ")"
	if idx := strings.LastIndex(description, " (available on servers:"); idx >= 0 {
		description = description[:idx]
	}
	return description + trailer
}

// GetAllResources returns a consolidated list of all resources from all connected servers.
//
// This method aggregates resources from all registered and connected servers, applying
// intelligent prefixing to resource URIs to avoid conflicts. Only servers that are
// currently connected contribute their resources to the result.
//
// Returns a slice of MCP resources ready for exposure through the aggregator.
func (r *ServerRegistry) GetAllResources() []mcp.Resource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allResources []mcp.Resource

	for serverName, info := range r.servers {
		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		for _, resource := range info.Resources {
			// Apply smart prefixing to resource URIs
			exposedResource := resource
			exposedResource.URI = r.ExposedResourceURI(serverName, resource.URI)
			allResources = append(allResources, exposedResource)
		}
		info.mu.RUnlock()
	}

	return allResources
}

// GetAllPrompts returns a consolidated list of all prompts from all connected servers.
//
// This method aggregates prompts from all registered and connected servers, applying
// intelligent prefixing to avoid name conflicts. Only servers that are currently
// connected contribute their prompts to the result.
//
// Returns a slice of MCP prompts ready for exposure through the aggregator.
func (r *ServerRegistry) GetAllPrompts() []mcp.Prompt {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allPrompts []mcp.Prompt

	for serverName, info := range r.servers {
		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		for _, prompt := range info.Prompts {
			// Apply smart prefixing to prompt names
			exposedPrompt := prompt
			exposedPrompt.Name = r.ExposedPromptName(serverName, prompt.Name)
			allPrompts = append(allPrompts, exposedPrompt)
		}
		info.mu.RUnlock()
	}

	return allPrompts
}

// ResolveToolName resolves an exposed (prefixed) tool name back to its source server and original name.
//
// This method is used when a tool call is received to determine which server should
// handle the request and what the original tool name was before prefixing. For
// family-grouped tools where multiple servers provide the same exposed name,
// callers must specify which server via ResolveToolNameForServer instead; this
// method returns an error noting that the "server" parameter is required.
//
// Args:
//   - exposedName: The prefixed tool name as seen by clients
//
// Returns the server name, original tool name, and nil error if resolution succeeds.
// Returns empty strings and an error if the name cannot be resolved, is ambiguous
// (family-grouped with multiple providers), or refers to a different item type.
func (r *ServerRegistry) ResolveToolName(exposedName string) (serverName, originalName string, err error) {
	r.nameMu.RLock()
	bucket := r.familyMappings[exposedName]
	m, soloOK := r.nameMapping[exposedName]
	r.nameMu.RUnlock()

	if bucket != nil && len(bucket.providers) > 1 {
		names := make([]string, len(bucket.providers))
		for i, p := range bucket.providers {
			names[i] = p.serverName
		}
		sort.Strings(names)
		return "", "", fmt.Errorf("tool %s is provided by family %s on servers %s; the %q parameter is required",
			exposedName, bucket.family, strings.Join(names, ", "), bucket.instanceArg)
	}
	if bucket != nil && len(bucket.providers) == 1 {
		return bucket.providers[0].serverName, bucket.providers[0].originalName, nil
	}
	if !soloOK {
		return "", "", fmt.Errorf("unknown name: %s", exposedName)
	}
	if m.itemType != metatools.ItemKindTool {
		return "", "", fmt.Errorf("name %s is a %s, not a tool", exposedName, m.itemType)
	}
	return m.serverName, m.originalName, nil
}

// ResolveToolNameForServer resolves a family-grouped or per-server exposed
// tool name when the caller already specified which backend server to route
// to (via the injected "server" arg). The returned originalName is the
// per-server tool name to forward to the backend.
//
// Returns an error if the exposed name is unknown OR if the requested
// serverName is not among the providers (for family tools) or not the owning
// server (for solo tools). Crucially, ambiguous family tools resolve here
// when given an explicit server, instead of falling back to legacy mappings.
func (r *ServerRegistry) ResolveToolNameForServer(exposedName, serverName string) (originalName string, err error) {
	r.nameMu.RLock()
	defer r.nameMu.RUnlock()
	if bucket, ok := r.familyMappings[exposedName]; ok {
		for _, p := range bucket.providers {
			if p.serverName == serverName {
				return p.originalName, nil
			}
		}
		available := make([]string, len(bucket.providers))
		for i, p := range bucket.providers {
			available[i] = p.serverName
		}
		sort.Strings(available)
		return "", fmt.Errorf("tool %s is not available on server %q (available: %s)",
			exposedName, serverName, strings.Join(available, ", "))
	}
	m, ok := r.nameMapping[exposedName]
	if !ok {
		return "", fmt.Errorf("unknown name: %s", exposedName)
	}
	if m.itemType != metatools.ItemKindTool {
		return "", fmt.Errorf("name %s is a %s, not a tool", exposedName, m.itemType)
	}
	if m.serverName != serverName {
		return "", fmt.Errorf("tool %s belongs to server %q, not %q", exposedName, m.serverName, serverName)
	}
	return m.originalName, nil
}

// IsFamilyTool reports whether the given exposed name is family-grouped
// (i.e. provided by one or more servers sharing a spec.family). Returns
// false for solo tools, core tools, and unknown names.
func (r *ServerRegistry) IsFamilyTool(exposedName string) bool {
	r.nameMu.RLock()
	defer r.nameMu.RUnlock()
	_, ok := r.familyMappings[exposedName]
	return ok
}

// FamilyInstanceArgFor returns the required instance-selector arg name for a
// family-grouped exposed tool, or empty string if the name is not family-
// grouped or unknown.
func (r *ServerRegistry) FamilyInstanceArgFor(exposedName string) string {
	r.nameMu.RLock()
	defer r.nameMu.RUnlock()
	if bucket, ok := r.familyMappings[exposedName]; ok {
		return bucket.instanceArg
	}
	return ""
}

// GetToolServerNames returns the set of server names that provide the given
// exposed tool name. Returns nil if the name is unknown. For family-grouped
// tools the slice has multiple entries (sorted); for solo tools a single
// entry; for prompts/resources or unmapped names, nil.
func (r *ServerRegistry) GetToolServerNames(exposedName string) []string {
	r.nameMu.RLock()
	defer r.nameMu.RUnlock()
	if bucket, ok := r.familyMappings[exposedName]; ok {
		out := make([]string, len(bucket.providers))
		for i, p := range bucket.providers {
			out[i] = p.serverName
		}
		sort.Strings(out)
		return out
	}
	if m, ok := r.nameMapping[exposedName]; ok && m.itemType == metatools.ItemKindTool {
		return []string{m.serverName}
	}
	return nil
}

// ResolvePromptName resolves an exposed (prefixed) prompt name back to its source server and original name.
//
// This method is used when a prompt request is received to determine which server should
// handle the request and what the original prompt name was before prefixing.
//
// Args:
//   - exposedName: The prefixed prompt name as seen by clients
//
// Returns the server name, original prompt name, and nil error if resolution succeeds.
// Returns empty strings and an error if the name cannot be resolved or refers to a different item type.
func (r *ServerRegistry) ResolvePromptName(exposedName string) (serverName, originalName string, err error) {
	r.nameMu.RLock()
	m, ok := r.nameMapping[exposedName]
	r.nameMu.RUnlock()
	if !ok {
		return "", "", fmt.Errorf("unknown name: %s", exposedName)
	}
	if m.itemType != metatools.ItemKindPrompt {
		return "", "", fmt.Errorf("name %s is a %s, not a prompt", exposedName, m.itemType)
	}
	return m.serverName, m.originalName, nil
}

// ResolveResourceName resolves an exposed (prefixed) resource URI back to its source server and original URI.
//
// This method is used when a resource read request is received to determine which server
// should handle the request and what the original resource URI was before prefixing.
//
// Args:
//   - exposedURI: The prefixed resource URI as seen by clients
//
// Returns the server name, original resource URI, and nil error if resolution succeeds.
// Returns empty strings and an error if the URI cannot be resolved or refers to a different item type.
func (r *ServerRegistry) ResolveResourceName(exposedURI string) (serverName, originalURI string, err error) {
	r.nameMu.RLock()
	m, ok := r.nameMapping[exposedURI]
	r.nameMu.RUnlock()
	if !ok {
		return "", "", fmt.Errorf("unknown name: %s", exposedURI)
	}
	if m.itemType != metatools.ItemKindResource {
		return "", "", fmt.Errorf("URI %s is a %s, not a resource", exposedURI, m.itemType)
	}
	return m.serverName, m.originalName, nil
}

// notifyUpdate sends a notification through the update channel to inform subscribers
// that the registry has been modified.
//
// This method is non-blocking - if the channel already has a pending notification,
// no additional notification is queued.
func (r *ServerRegistry) notifyUpdate() {
	select {
	case r.updateChan <- struct{}{}:
	default:
		// Channel already has a notification pending
	}
}

// GetUpdateChannel returns a read-only channel that receives notifications when
// the registry is updated.
//
// Subscribers can use this channel to react to server registrations, deregistrations,
// or capability changes. The channel is buffered with a capacity of 1 to prevent
// blocking the registry operations.
//
// Returns a receive-only channel for registry update notifications.
func (r *ServerRegistry) GetUpdateChannel() <-chan struct{} {
	return r.updateChan
}

// GetServerInfo returns detailed information about a specific registered server.
//
// This method provides access to the complete ServerInfo structure for a given
// server, including its client, cached capabilities, and connection status.
//
// Args:
//   - name: Unique identifier of the server
//
// Returns the ServerInfo pointer and true if the server exists.
// Returns nil and false if the server is not found.
func (r *ServerRegistry) GetServerInfo(name string) (*ServerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.servers[name]
	return info, exists
}

// GetAllServers returns a copy of all registered server information.
//
// This method provides a snapshot of all servers currently registered with
// the registry, including both connected and disconnected servers. The returned
// map is a copy to prevent external modifications to the internal state.
//
// Returns a map of server names to their corresponding ServerInfo structures.
func (r *ServerRegistry) GetAllServers() map[string]*ServerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create a copy to prevent external modifications
	result := make(map[string]*ServerInfo, len(r.servers))
	for k, v := range r.servers {
		result[k] = v
	}
	return result
}

// refreshServerCapabilities queries a server for its current capabilities and updates the cache.
//
// This method fetches tools, resources, and prompts from the specified server and updates
// the cached information. It handles partial failures gracefully - if one type of capability
// cannot be retrieved, the others are still updated.
//
// Args:
//   - ctx: Context for the capability queries
//   - info: ServerInfo structure to update with fresh capabilities
//
// Returns an error only if the tool query fails (tools are considered mandatory).
// Resource and prompt query failures are logged but not treated as errors.
func (r *ServerRegistry) refreshServerCapabilities(ctx context.Context, info *ServerInfo) error {
	// Get tools (considered mandatory)
	tools, err := info.Client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}
	info.UpdateTools(tools)

	// Get resources (optional - some servers may not support resources)
	resources, err := info.Client.ListResources(ctx)
	if err != nil {
		// Resources might not be supported by this server
		logging.Debug("Aggregator", "Failed to list resources for %s: %v", info.Name, err)
		info.UpdateResources([]mcp.Resource{})
	} else {
		info.UpdateResources(resources)
	}

	// Get prompts (optional - some servers may not support prompts)
	prompts, err := info.Client.ListPrompts(ctx)
	if err != nil {
		// Prompts might not be supported by this server
		logging.Debug("Aggregator", "Failed to list prompts for %s: %v", info.Name, err)
		info.UpdatePrompts([]mcp.Prompt{})
	} else {
		info.UpdatePrompts(prompts)
	}

	return nil
}

// RegisterPendingAuth registers a server that is reachable but requires
// authentication before its tools can be exposed. Per ADR-008, no synthetic
// authentication tools are created — users authenticate via core_auth_login.
//
// AuthConfig in the registration may be nil; in either case the server is
// flagged as requiring per-session authentication.
func (r *ServerRegistry) RegisterPendingAuth(registration PendingAuthRegistration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[registration.Name]; exists {
		return fmt.Errorf("server %s already registered", registration.Name)
	}

	authConfig := registration.AuthConfig
	if authConfig == nil {
		authConfig = &api.MCPServerAuth{}
	}

	info := &ServerInfo{
		Name:       registration.Name,
		URL:        registration.URL,
		ToolPrefix: registration.ToolPrefix,
		Family:     cloneFamily(registration.Family),
		AuthInfo:   registration.AuthInfo,
		AuthConfig: authConfig,
	}

	r.applyServerRegistrationLocked(registration.Name, registration.ToolPrefix, registration.Family)

	r.servers[registration.Name] = info
	r.notifyUpdate()

	if authConfig.ForwardToken {
		logging.Info("Aggregator", "Registered pending auth server: %s (requires auth, SSO token forwarding enabled)", registration.Name)
	} else {
		logging.Info("Aggregator", "Registered pending auth server: %s (requires authentication, use core_auth_login)", registration.Name)
	}
	return nil
}

// GetAllToolsForSession returns the tools visible to a specific login session.
//
// For OAuth servers (RequiresSessionAuth), tools are read from the CapabilityStore
// keyed by session ID (token family). For non-OAuth servers, tools are read from
// ServerInfo.Tools (same as GetAllTools). Family grouping is applied to the
// resulting union so a user who is authenticated against multiple instances
// of the same family sees a single deduplicated tool with the "server" enum.
func (r *ServerRegistry) GetAllToolsForSession(ctx context.Context, store oauthstore.CapabilityStore, sessionID string) []mcp.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var contributions []serverToolContribution

	for serverName, info := range r.servers {
		if info.RequiresSessionAuth() {
			if store == nil {
				continue
			}
			caps, err := store.Get(ctx, sessionID, serverName)
			if err != nil || caps == nil {
				continue
			}
			contributions = append(contributions, serverToolContribution{
				serverName: serverName,
				family:     cloneFamily(info.Family),
				tools:      append([]mcp.Tool(nil), caps.Tools...),
			})
			continue
		}

		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		toolsCopy := append([]mcp.Tool(nil), info.Tools...)
		info.mu.RUnlock()
		contributions = append(contributions, serverToolContribution{
			serverName: serverName,
			family:     cloneFamily(info.Family),
			tools:      toolsCopy,
		})
	}

	return r.assembleExposedTools(contributions)
}

// GetAllResourcesForSession returns the resources visible to a specific login session.
//
// For OAuth servers, resources are read from the CapabilityStore.
// For non-OAuth servers, resources are read from ServerInfo.Resources.
func (r *ServerRegistry) GetAllResourcesForSession(ctx context.Context, store oauthstore.CapabilityStore, sessionID string) []mcp.Resource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allResources []mcp.Resource

	for serverName, info := range r.servers {
		if info.RequiresSessionAuth() {
			if store == nil {
				continue
			}
			caps, err := store.Get(ctx, sessionID, serverName)
			if err != nil || caps == nil {
				continue
			}
			for _, resource := range caps.Resources {
				exposedResource := resource
				exposedResource.URI = r.ExposedResourceURI(serverName, resource.URI)
				allResources = append(allResources, exposedResource)
			}
			continue
		}

		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		for _, resource := range info.Resources {
			exposedResource := resource
			exposedResource.URI = r.ExposedResourceURI(serverName, resource.URI)
			allResources = append(allResources, exposedResource)
		}
		info.mu.RUnlock()
	}

	return allResources
}

// GetAllPromptsForSession returns the prompts visible to a specific login session.
//
// For OAuth servers, prompts are read from the CapabilityStore.
// For non-OAuth servers, prompts are read from ServerInfo.Prompts.
func (r *ServerRegistry) GetAllPromptsForSession(ctx context.Context, store oauthstore.CapabilityStore, sessionID string) []mcp.Prompt {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allPrompts []mcp.Prompt

	for serverName, info := range r.servers {
		if info.RequiresSessionAuth() {
			if store == nil {
				continue
			}
			caps, err := store.Get(ctx, sessionID, serverName)
			if err != nil || caps == nil {
				continue
			}
			for _, prompt := range caps.Prompts {
				exposedPrompt := prompt
				exposedPrompt.Name = r.ExposedPromptName(serverName, prompt.Name)
				allPrompts = append(allPrompts, exposedPrompt)
			}
			continue
		}

		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		for _, prompt := range info.Prompts {
			exposedPrompt := prompt
			exposedPrompt.Name = r.ExposedPromptName(serverName, prompt.Name)
			allPrompts = append(allPrompts, exposedPrompt)
		}
		info.mu.RUnlock()
	}

	return allPrompts
}

// GetOAuthServers returns a list of servers that require OAuth authentication.
func (r *ServerRegistry) GetOAuthServers() []*ServerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var servers []*ServerInfo
	for _, info := range r.servers {
		if info.RequiresSessionAuth() {
			servers = append(servers, info)
		}
	}
	return servers
}

// IsOAuthServer checks if a server requires OAuth authentication.
func (r *ServerRegistry) IsOAuthServer(serverName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.servers[serverName]
	if !exists {
		return false
	}
	return info.RequiresSessionAuth()
}
