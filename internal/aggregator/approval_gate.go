package aggregator

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Enforcement of the effective approval (approval.go resolves and reports
// it). Every call is gated unless muster marks it its own
// (api.WithoutApprovalGate); at dispatch, once the target is resolved, the call
// runs when its effective approval is Allow, is refused as not found when it
// is Block, and asks when it is Ask: the call is recorded as a pending
// approval and answered with the approval page's link (approval_answer.go),
// and only a retry that finds the matching approved entry runs, once, with
// the arguments the person saw. A workflow asks once, up front, for all of
// its steps (approval_workflow.go); its steps are then checked against that
// up-front approval, never asked one by one.

// DefaultApprovalTTL is how long a pending approval can be decided and its
// approval used.
const DefaultApprovalTTL = 5 * time.Minute

// approvalSettings is the aggregator's approval configuration.
type approvalSettings struct {
	// page builds the approval links; nil without a public URL.
	page *approval.Page
	// ttl is how long a request lives.
	ttl time.Duration
}

// approvalUnavailable returns why muster cannot ask here, "" when it can.
func (a *AggregatorServer) approvalUnavailable(ctx context.Context) string {
	switch {
	case api.GetApprovalStore() == nil:
		return "muster's shared store is not on Valkey (aggregator.oauth.server.storage.type: valkey)"
	case api.GetBrowserIdentity() == nil:
		return "muster runs no browser sign-in (its own OAuth server)"
	case a.approval.page == nil:
		return "muster has no public URL for its approval page (aggregator.oauth.mcpClient.publicUrl)"
	case api.SignInIdentityFromContext(ctx) == (api.SignInIdentity{}):
		return "muster cannot tell which person this call is for"
	}
	return ""
}

// serverToolApproval resolves the effective approval of a server's tool for
// the caller: the server's admin setting, made stricter for an agent caller
// by a floor or governance entry. A floor's or governed name on a server the
// governance configuration does not name is refused to agents.
func serverToolApproval(ctx context.Context, server, original string, args map[string]any) (*api.MCPServerInfo, api.ToolApproval, error) {
	if problem := api.ApprovalSchemaProblem(ctx); problem != "" {
		return nil, api.ToolApproval{}, fmt.Errorf("approval_unavailable: %s", problem)
	}
	info, err := connectorInfo(ctx, server)
	if err != nil {
		return nil, api.ToolApproval{}, fmt.Errorf("the approval settings of server %s could not be read: %w", server, err)
	}
	var settings *api.MCPServerApproval
	if info != nil {
		settings = info.Approval
	}
	effective := api.ResolveConnectorApproval(settings, original)
	if effective.Setting == api.ApprovalSettingBlock {
		return info, effective, nil
	}
	if api.IsAgentCaller(api.CallerKind(ctx, getSessionIDFromContext(ctx))) {
		if info != nil && info.Type == string(api.MCPServerTypeStdio) && api.GetGovernance().NamesServer(server) {
			return info, api.ToolApproval{}, fmt.Errorf("agents may not call server %s over stdio: muster's governance configuration names it, and a stdio connection cannot tell it that an agent calls (X-Muster-Caller-Kind)", server)
		}
		floor, applies, refused := api.GetGovernance().ServerFloor(server, original, args)
		if refused {
			return info, api.ToolApproval{}, fmt.Errorf("agents may not call %s on server %s: muster's governance configuration does not name that server for it", original, server)
		}
		if applies {
			effective = api.StrictestApproval(effective, floor)
		}
	}
	return info, effective, nil
}

// connectorInfo reads one server's definition; nil without a manager. A
// server the manager does not find is an error: its registry entry outlived
// its object (deleted, or no longer served), and its settings, Block
// included, cannot be known, so it must not resolve to Allow.
func connectorInfo(ctx context.Context, serverKey string) (*api.MCPServerInfo, error) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, nil
	}
	info, err := manager.GetMCPServer(ctx, serverKey)
	if err != nil {
		if api.IsNotFound(err) {
			return nil, fmt.Errorf("its MCPServer object cannot be found (deleted, or no longer served)")
		}
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("its MCPServer object cannot be found (deleted, or no longer served)")
	}
	return info, nil
}

// coreToolApproval resolves the effective approval of one of muster's core
// tools: its own configuration always asks; a floor or governance entry asks
// agent callers; every other core tool runs.
func coreToolApproval(ctx context.Context, name string, args map[string]any) api.ToolApproval {
	if builtin, ok := api.BuiltinApproval(name); ok {
		return builtin
	}
	if api.IsAgentCaller(api.CallerKind(ctx, getSessionIDFromContext(ctx))) {
		if floor, ok := api.GetGovernance().CoreFloor(name, args); ok {
			return floor
		}
	}
	return api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}
}

// coreName returns a core tool's name with its core_ prefix, the form the
// built-in settings and floors use whichever form the call used.
func coreName(toolName string) string {
	return "core_" + strings.TrimPrefix(toolName, "core_")
}

// gateServerTool applies the effective approval to a call of a server's tool
// about to be dispatched. It returns the arguments to dispatch (the approved
// ones on a retry), or the answer to give instead (the approval link, a
// declined result), or an error refusing the call.
func (a *AggregatorServer) gateServerTool(ctx context.Context, toolName, server, original string, args map[string]any) (map[string]any, *mcp.CallToolResult, error) {
	run := workflowRunFromContext(ctx)
	info, effective, err := serverToolApproval(ctx, server, original, args)
	target := api.ApprovalTarget{Kind: api.ApprovalTargetTool, Server: server, Name: original, Exposed: toolName}
	if err != nil {
		a.auditApproval(ctx, "call_refused", target, "", "", err.Error(), effective)
		return nil, nil, fmt.Errorf("tool %s cannot be called: %w", toolName, err)
	}
	if effective.Setting == api.ApprovalSettingBlock {
		a.auditApproval(ctx, "call_refused", target, "", "", "blocked by the server's approval settings", effective)
		return nil, nil, fmt.Errorf("tool not found: %s", toolName)
	}
	if run == nil && !api.ApprovalGateFromContext(ctx) {
		// muster's own call, on no client's behalf: Block still holds.
		return args, nil, nil
	}
	if run != nil {
		return args, nil, run.check(toolName, effective)
	}
	if effective.Setting == api.ApprovalSettingAllow {
		return args, nil, nil
	}
	if answer, err, needed := a.signInFirst(ctx, server, original, args); needed {
		return nil, answer, err
	}
	if info != nil {
		target.UID, target.Generation = info.UID, info.Generation
	}
	return a.ask(ctx, target, effective, args, func() api.ApprovalDisplay {
		return a.toolDisplay(ctx, toolName, info, original, args)
	})
}

// signInFirst answers a call that asks with the server's sign-in when the
// session must sign in to the server before the call could run: asking
// first would use the approval up on a call that then cannot run, and the
// person would be asked twice for one call (M1-MU-C-09). needed is false
// when the session can call the server.
func (a *AggregatorServer) signInFirst(ctx context.Context, server, original string, args map[string]any) (*mcp.CallToolResult, error, bool) {
	serverInfo, exists := a.registry.GetServerInfo(server)
	sessionID := getSessionIDFromContext(ctx)
	if !exists || serverInfo == nil || !serverInfo.RequiresSessionAuth() || sessionID == "" {
		return nil, nil, false
	}
	sub := getUserSubjectFromContext(ctx)
	err := a.requireSessionAuth(ctx, serverInfo, sessionID, sub)
	notAuthenticated, ok := errors.AsType[*sessionNotAuthenticatedError](err)
	if !ok {
		return nil, nil, false
	}
	answer, err := a.authRequiredAnswer(ctx, server, original, args, sessionID, sub, notAuthenticated.reason())
	return answer, err, true
}

// gateCoreTool applies the effective approval to a call of a core tool.
// Workflow runs are gated by gateWorkflow.
func (a *AggregatorServer) gateCoreTool(ctx context.Context, toolName string, args map[string]any) (map[string]any, *mcp.CallToolResult, error) {
	run := workflowRunFromContext(ctx)
	if run == nil && !api.ApprovalGateFromContext(ctx) {
		return args, nil, nil
	}
	name := coreName(toolName)
	effective := coreToolApproval(ctx, name, args)
	if run != nil {
		return args, nil, run.check(toolName, effective)
	}
	if effective.Setting == api.ApprovalSettingAllow {
		return args, nil, nil
	}
	if name == "core_mcpserver_set_approval" {
		args = a.bindPreset(ctx, args)
	}
	target := api.ApprovalTarget{Kind: api.ApprovalTargetCore, Name: name, Exposed: toolName}
	return a.ask(ctx, target, effective, args, func() api.ApprovalDisplay {
		return a.coreDisplay(ctx, name, args)
	})
}

// bindPreset turns a core_mcpserver_set_approval call carrying a preset into
// the explicit settings the preset expands to now, from the tools the server
// lists. The person approves those settings, and those are what runs: a
// server that changes its tools' annotations between the page and the retry
// makes the retry another call, which asks again, instead of writing an
// expansion nobody saw (M1-MU-S-07). An unknown preset is left for the tool
// to refuse.
func (a *AggregatorServer) bindPreset(ctx context.Context, args map[string]any) map[string]any {
	preset, ok := args["preset"].(string)
	if !ok {
		return args
	}
	if _, both := args["approval"]; both {
		return args
	}
	server, _ := args["name"].(string)
	tools, _ := a.ServerTools(ctx, server)
	expanded, err := api.ExpandApprovalPreset(preset, tools)
	if err != nil {
		return args
	}
	settings := map[string]any{"rule": expanded.Rule}
	if len(expanded.Tools) > 0 {
		entries := make(map[string]any, len(expanded.Tools))
		for tool, setting := range expanded.Tools {
			entries[tool] = setting
		}
		settings["tools"] = entries
	}
	bound := make(map[string]any, len(args))
	for key, value := range args {
		if key != "preset" {
			bound[key] = value
		}
	}
	bound["approval"] = settings
	return bound
}

// ask runs a call that asks: with an approved entry bound to this caller,
// target and arguments, the approved arguments are dispatched (the entry is
// used up); a declined one answers declined; otherwise the call is answered
// with the approval link of a pending request, a new one or the identical
// call's.
func (a *AggregatorServer) ask(ctx context.Context, target api.ApprovalTarget, effective api.ToolApproval, args map[string]any, display func() api.ApprovalDisplay) (map[string]any, *mcp.CallToolResult, error) {
	name := target.Exposed
	if name == "" {
		name = target.Name
	}
	if reason := a.approvalUnavailable(ctx); reason != "" {
		a.auditApproval(ctx, "approval_refused", target, "", "", reason, effective)
		return nil, nil, fmt.Errorf("approval_unavailable: %s asks before it runs (%s from %s), and %s, so it cannot run", name, effective.Setting, effective.Source, reason)
	}
	canonical, hash, err := approval.Canonical(args)
	if err != nil {
		return nil, nil, fmt.Errorf("%s asks before it runs, and its arguments cannot be shown for approval: %w", name, err)
	}
	store := api.GetApprovalStore()
	sessionID := getSessionIDFromContext(ctx)
	identity := api.SignInIdentityFromContext(ctx)
	key := api.ApprovalBindingKey(identity, sessionID, "", target, hash)

	request, err := store.Consume(ctx, key)
	switch {
	case err == nil && request.Status == api.ApprovalStatusApproved:
		approved, decodeErr := approval.DecodeCanonical(request.Arguments)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		a.auditApproval(ctx, "approval_used", target, request.ID, hash, "", effective)
		return approved, nil, nil
	case err == nil && request.Status == api.ApprovalStatusDeclined:
		a.auditApproval(ctx, "approval_declined", target, request.ID, hash, "", effective)
		return nil, declinedResult(name), nil
	case err == nil:
		return nil, a.recordApprovalChallenge(ctx, request), nil
	case !errors.Is(err, api.ErrApprovalNotFound):
		a.auditApproval(ctx, "approval_refused", target, "", hash, "store unavailable", effective)
		return nil, nil, fmt.Errorf("approval_unavailable: %s asks before it runs, and muster's approval store cannot be reached, so it cannot run: %w", name, err)
	}

	shown := display()
	shown.Setting, shown.Source = effective.Setting, effective.Source
	now := clock.Now()
	request, err = store.Create(ctx, &api.ApprovalRequest{
		ID:            rand.Text(),
		ElicitationID: rand.Text(),
		Identity:      identity,
		SessionID:     sessionID,
		CallerKind:    api.CallerKind(ctx, sessionID),
		Target:        target,
		Arguments:     canonical,
		ArgumentsHash: hash,
		Display:       shown,
		CreatedAt:     now,
		ExpiresAt:     now.Add(a.approval.ttl),
	})
	switch {
	case errors.Is(err, api.ErrApprovalLimit):
		a.auditApproval(ctx, "approval_refused", target, "", hash, "too many pending approvals", effective)
		return nil, nil, fmt.Errorf("%s asks before it runs, and %s: decide or decline those at their approval links first, or wait until they expire (after at most %s)", name, limitReason(err), a.approval.ttl)
	case err != nil:
		a.auditApproval(ctx, "approval_refused", target, "", hash, "store unavailable", effective)
		return nil, nil, fmt.Errorf("approval_unavailable: %s asks before it runs, and muster's approval store cannot be reached, so it cannot run: %w", name, err)
	}
	a.auditApproval(ctx, "approval_requested", target, request.ID, hash, "", effective)
	return nil, a.recordApprovalChallenge(ctx, request), nil
}

// limitReason says which cap refused a request.
func limitReason(err error) string {
	var limit *api.ApprovalLimitError
	if errors.As(err, &limit) && limit.Scope == api.ApprovalLimitPerson {
		return fmt.Sprintf("%d of this person's calls already wait for a decision, across all their sessions and agents", limit.Limit)
	}
	if limit != nil {
		return fmt.Sprintf("%d of this session's calls already wait for a decision", limit.Limit)
	}
	return "too many calls already wait for a decision"
}

// declinedResult answers a retry of a call the person declined.
func declinedResult(name string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("declined: the person declined %s. It did not run.", name))
}

// auditApproval records a request, refusal or use of an approval: who,
// which caller kind, the approval id when there is one, the target, why it
// asks and the arguments' hash (never the arguments).
func (a *AggregatorServer) auditApproval(ctx context.Context, action string, target api.ApprovalTarget, id, hash, reason string, effective api.ToolApproval) {
	attrs := []slog.Attr{
		slog.String("session", getSessionIDFromContext(ctx)),
		slog.String("targetKind", target.Kind),
		slog.String("server", target.Server),
		slog.String("target", target.Name),
		slog.String("setting", effective.Setting),
		slog.String("source", effective.Source),
	}
	if id != "" {
		attrs = append(attrs, slog.String("approval", id))
	}
	if hash != "" {
		attrs = append(attrs, slog.String("argumentsHash", hash))
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	api.AuditApproval(ctx, action, attrs...)
}

// newApprovalSettings registers the approval store when muster's shared
// store is on Valkey: approval is single use across replicas only there. On
// the in-memory stores none is registered and every call that asks is
// refused.
func newApprovalSettings(cfg AggregatorConfig, stores storeBundle) approvalSettings {
	settings := approvalSettings{ttl: cfg.ApprovalTTL}
	if settings.ttl <= 0 {
		settings.ttl = DefaultApprovalTTL
	}
	if stores.valkeyClient != nil {
		api.RegisterApprovalStore(approval.NewValkeyStore(stores.valkeyClient, stores.keyPrefix, stores.encryptor))
	} else {
		api.RegisterApprovalStore(nil)
	}
	return settings
}

// registerApprovalPage mounts the approval page on muster's public URL (the
// OAuth proxy's publicUrl, else the OAuth server's baseUrl, which must share
// its origin: the page's sign-in returns to it).
func (a *AggregatorServer) registerApprovalPage(mux *http.ServeMux, baseURL string) {
	publicURL := a.config.OAuth.PublicURL
	if publicURL == "" {
		publicURL = baseURL
	}
	if publicURL == "" {
		logging.Warn("Aggregator", "No public URL: the approval page is not served and calls that ask are refused")
		return
	}
	page := &approval.Page{
		PublicURL:       publicURL,
		Installation:    a.config.ApprovalInstallation,
		Store:           api.GetApprovalStore,
		Identity:        api.GetBrowserIdentity,
		RedirectAllowed: api.PostLoginRedirectAllowed,
	}
	page.Register(mux)
	a.approval.page = page
	logging.InfoWithAttrs("Aggregator", "Mounted the approval page", slog.String("path", approval.PathPrefix+"{id}"))
}
