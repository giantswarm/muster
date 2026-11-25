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
	if err != nil {
		return nil, nil, fmt.Errorf("tool %s cannot be called: %w", toolName, err)
	}
	if effective.Setting == api.ApprovalSettingBlock {
		logging.InfoWithAttrs("Aggregator", "call refused: the tool is blocked by its server's approval settings",
			slog.String("tool", toolName), slog.String("server", server), slog.String("original", original))
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
	target := api.ApprovalTarget{Kind: api.ApprovalTargetTool, Server: server, Name: original, Exposed: toolName}
	if info != nil {
		target.UID, target.Generation = info.UID, info.Generation
	}
	return a.ask(ctx, target, effective, args, func() api.ApprovalDisplay {
		return a.toolDisplay(ctx, toolName, info, original, args)
	})
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
	target := api.ApprovalTarget{Kind: api.ApprovalTargetCore, Name: name, Exposed: toolName}
	return a.ask(ctx, target, effective, args, func() api.ApprovalDisplay {
		return a.coreDisplay(ctx, name, args)
	})
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
		a.auditApproval(ctx, "approval_refused", target, "", reason)
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
		a.auditApproval(ctx, "approval_used", target, hash, "")
		return approved, nil, nil
	case err == nil && request.Status == api.ApprovalStatusDeclined:
		a.auditApproval(ctx, "approval_declined", target, hash, "")
		return nil, declinedResult(name), nil
	case err == nil:
		return nil, a.recordApprovalChallenge(ctx, request), nil
	case !errors.Is(err, api.ErrApprovalNotFound):
		a.auditApproval(ctx, "approval_refused", target, hash, "store unavailable")
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
		a.auditApproval(ctx, "approval_refused", target, hash, "too many pending approvals")
		return nil, nil, fmt.Errorf("%s asks before it runs, and too many of this session's calls already wait for a person: decide those first", name)
	case err != nil:
		a.auditApproval(ctx, "approval_refused", target, hash, "store unavailable")
		return nil, nil, fmt.Errorf("approval_unavailable: %s asks before it runs, and muster's approval store cannot be reached, so it cannot run: %w", name, err)
	}
	a.auditApproval(ctx, "approval_requested", target, hash, "")
	return nil, a.recordApprovalChallenge(ctx, request), nil
}

// declinedResult answers a retry of a call the person declined.
func declinedResult(name string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("declined: the person declined %s. It did not run.", name))
}

// auditApproval records a request, refusal or use of an approval: who, which
// caller kind, which target and the arguments' hash (never the arguments).
func (a *AggregatorServer) auditApproval(ctx context.Context, action string, target api.ApprovalTarget, hash, reason string) {
	identity := api.SignInIdentityFromContext(ctx)
	sessionID := getSessionIDFromContext(ctx)
	attrs := []slog.Attr{
		slog.String("action", action),
		slog.String("requester", identity.Issuer+"#"+identity.Subject),
		slog.String("callerKind", api.CallerKind(ctx, sessionID)),
		slog.String("session", logging.TruncateIdentifier(sessionID)),
		slog.String("targetKind", target.Kind),
		slog.String("server", target.Server),
		slog.String("target", target.Name),
		slog.String("governance", api.GetGovernance().Mode),
	}
	if hash != "" {
		attrs = append(attrs, slog.String("argumentsHash", hash))
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	logging.InfoWithAttrsCtx(ctx, "Approval", "[AUDIT] "+action, attrs...)
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
