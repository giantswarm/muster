package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// A call whose effective approval is Ask runs only after a person approved
// that exact call on muster's approval page. The call is answered with a link
// to the page and recorded as a pending approval (ApprovalRequest) in muster's
// shared store; the person decides on the page; the client calls again, and
// the retry runs once, with the arguments the person saw, when the store holds
// an approved entry bound to the same caller, target and arguments.

// The decision states of an approval request.
const (
	ApprovalStatusPending  = "pending"
	ApprovalStatusApproved = "approved"
	ApprovalStatusDeclined = "declined"
)

// The kinds of target an approval binds.
const (
	// ApprovalTargetTool is a tool of a registered MCP server.
	ApprovalTargetTool = "tool"
	// ApprovalTargetCore is one of muster's own core tools.
	ApprovalTargetCore = "core"
	// ApprovalTargetWorkflow is a workflow run, approved once up front.
	ApprovalTargetWorkflow = "workflow"
)

// The kinds of caller, as the audit records them. Only agent callers are
// asked by the floors and the governance entries; the admin settings ask
// every caller.
const (
	// CallerKindPerson is a person acting directly with their own bearer
	// (the portal backend, a person's own client).
	CallerKindPerson = "person bearer"
	// CallerKindActor is a Session sandbox calling with its actor JWT.
	CallerKindActor = "actor JWT"
	// CallerKindDelegated is a caller presenting a delegated token whose
	// act claim names the party acting for the person.
	CallerKindDelegated = "delegated"
)

// CallerKind returns the kind of caller the request carries, with its
// evidence: an actor session, a delegated token, or else a person's bearer.
func CallerKind(ctx context.Context, sessionID string) string {
	switch {
	case IsActorSession(sessionID):
		return CallerKindActor
	default:
		if _, ok := DelegatedCallerFromContext(ctx); ok {
			return CallerKindDelegated
		}
		return CallerKindPerson
	}
}

// IsAgentCaller reports whether the caller kind is an agent's.
func IsAgentCaller(kind string) bool {
	return kind == CallerKindActor || kind == CallerKindDelegated
}

// ApprovalTarget is what a call dispatches to, never the name the client
// sent: the owning MCPServer and its own tool name, one of muster's core
// tools, or a workflow and its definition.
type ApprovalTarget struct {
	Kind string `json:"kind"`
	// Server is the server key of the owning MCPServer (a family call: the
	// selected member); empty for core tools and workflows.
	Server string `json:"server,omitempty"`
	// UID and Generation identify the MCPServer or workflow object, so a
	// replaced or changed definition no longer matches; empty and zero in
	// filesystem mode.
	UID        string `json:"uid,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	// Name is the server's own tool name, the core tool's name, or the
	// workflow's name.
	Name string `json:"name"`
	// Exposed is the name the tool is exposed under, for display only; it is
	// not part of the binding.
	Exposed string `json:"exposed,omitempty"`
	// Definition is the hash of a workflow's definition (inputs, steps,
	// cleanup steps): a changed definition is a different target.
	Definition string `json:"definition,omitempty"`
}

// bindingKey returns the target's part of an approval's binding.
func (t ApprovalTarget) bindingKey() string {
	return strings.Join([]string{t.Kind, t.Server, t.UID, strconv.FormatInt(t.Generation, 10), t.Name, t.Definition}, "\x00")
}

// ApprovalArgument is one labelled value shown on the approval page.
type ApprovalArgument struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Value is the value in canonical JSON (a string quoted).
	Value string `json:"value"`
	// Type is the value's JSON type (string, number, boolean, null, object,
	// array), shown beside it; empty for values that are not arguments.
	Type string `json:"type,omitempty"`
}

// ApprovalChange is one field an edit changes, old to new.
type ApprovalChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// ApprovalStep is one step of a workflow on the approval page.
type ApprovalStep struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Tool  string `json:"tool"`
	Asks  bool   `json:"asks,omitempty"`
	// Workflow names the nested workflow the step belongs to, empty for a
	// step of the workflow being approved.
	Workflow string `json:"workflow,omitempty"`
	// Arguments are the step's arguments as the definition builds them
	// (templates shown as written).
	Arguments []ApprovalArgument `json:"arguments,omitempty"`
}

// ApprovalDisplay is what the approval page shows, worked out by muster from
// the target's own definition when the request is created: never words an
// agent chose.
type ApprovalDisplay struct {
	// Message is the plain-words summary the elicitation carries.
	Message string `json:"message"`
	// Title is the tool's MCP title, or its name made readable.
	Title string `json:"title"`
	// Connector names the server, its display name when it has one.
	Connector string `json:"connector,omitempty"`
	// ChangesData is true for a tool not annotated read-only.
	ChangesData bool `json:"changesData,omitempty"`
	// Arguments are the call's canonical arguments, labelled.
	Arguments []ApprovalArgument `json:"arguments,omitempty"`
	// Changes are the fields an edit changes, when muster could read the
	// current object; empty when it could not (the arguments are shown).
	Changes []ApprovalChange `json:"changes,omitempty"`
	// Preset lists the per-tool settings a preset expands to.
	Preset []ApprovalArgument `json:"preset,omitempty"`
	// Steps are a workflow's steps, the asking ones marked.
	Steps []ApprovalStep `json:"steps,omitempty"`
	// Setting and Source say why the call asks.
	Setting string `json:"setting"`
	Source  string `json:"source"`
}

// ApprovalRequest is a pending approval: bound to the person and Session the
// call came from, the resolved target and the canonical arguments, single use
// and expiring.
type ApprovalRequest struct {
	// ID is the random, unguessable id in the approval page's URL.
	ID string `json:"id"`
	// ElicitationID is the elicitation's own random id, distinct from ID, the
	// one the page's return carries.
	ElicitationID string `json:"elicitationId"`

	// Identity is the person the call was made for (issuer and raw sub):
	// only a browser signed in as them may decide.
	Identity SignInIdentity `json:"identity"`
	// SessionID is the muster session the call came from (an actor's
	// session for a Session sandbox).
	SessionID string `json:"sessionId"`
	// Agent names the calling agent where muster knows it.
	Agent string `json:"agent,omitempty"`
	// CallerKind is the caller's kind and its evidence.
	CallerKind string `json:"callerKind"`

	Target ApprovalTarget `json:"target"`
	// Arguments are the RFC 8785 canonical form of the arguments muster
	// dispatches; ArgumentsHash is their SHA-256.
	Arguments     string `json:"arguments"`
	ArgumentsHash string `json:"argumentsHash"`

	Display ApprovalDisplay `json:"display"`

	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`

	// Status, DecidedBy and DecidedAt are filled when the store reports the
	// decision; they are not stored with the request.
	Status    string         `json:"-"`
	DecidedBy SignInIdentity `json:"-"`
}

// BindingKey is the hash of everything the approval is bound to: identity,
// Session and agent, target and canonical arguments. A retry runs only an
// approval with the same key.
func (r *ApprovalRequest) BindingKey() string {
	return ApprovalBindingKey(r.Identity, r.SessionID, r.Agent, r.Target, r.ArgumentsHash)
}

// ApprovalBindingKey returns the binding key of a call.
func ApprovalBindingKey(identity SignInIdentity, sessionID, agent string, target ApprovalTarget, argumentsHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		identity.Issuer, identity.Subject, sessionID, agent, target.bindingKey(), argumentsHash,
	}, "\x00\x00")))
	return hex.EncodeToString(sum[:])
}

// Errors of the approval store.
var (
	// ErrApprovalNotFound is a request that does not exist or expired.
	ErrApprovalNotFound = errors.New("approval request not found or expired")
	// ErrApprovalDecided is a decision on a request already decided.
	ErrApprovalDecided = errors.New("approval request already decided")
	// ErrApprovalLimit is a request beyond the caps on pending requests.
	ErrApprovalLimit = errors.New("too many pending approval requests")
)

// ApprovalPageFlow is the approval page's state for one browser and one
// request: the sign-in that tells muster who is in the browser, the
// anti-forgery token of the decision form and the return the person's client
// asked for. It is kept per browser, never on the request, so one person's
// return cannot be swapped for another's.
type ApprovalPageFlow struct {
	// Nonce names the identity sign-in in flight.
	Nonce string `json:"nonce"`
	// Identity is who signed in in this browser, once the sign-in returned.
	Identity SignInIdentity `json:"identity"`
	// Token is the anti-forgery token the decision form carries.
	Token string `json:"token"`
	// Redirect is the allowlisted return the client asked for, used only
	// after a decision.
	Redirect string `json:"redirect,omitempty"`
}

// ApprovalStore keeps approval requests, their decisions and the page's
// browser flows. Implementations shared by replicas (Valkey) make an approval
// decided on one replica usable on another; every operation fails rather
// than guesses when the store is unavailable.
type ApprovalStore interface {
	// Create stores a pending request, unless one with the same binding is
	// still pending or decided, which is returned instead (an identical call
	// reuses its link). It refuses with ErrApprovalLimit beyond the caps.
	Create(ctx context.Context, request *ApprovalRequest) (*ApprovalRequest, error)
	// Find returns the request bound to bindingKey with its status, without
	// consuming it; ErrApprovalNotFound when there is none.
	Find(ctx context.Context, bindingKey string) (*ApprovalRequest, error)
	// Consume atomically takes the decision of the request bound to
	// bindingKey and removes the request: of concurrent retries exactly one
	// gets it. A request not decided yet is returned pending and kept;
	// ErrApprovalNotFound when there is none.
	Consume(ctx context.Context, bindingKey string) (*ApprovalRequest, error)
	// Get returns the request by its id with its status.
	Get(ctx context.Context, id string) (*ApprovalRequest, error)
	// Decide records the decision on a pending request, once.
	Decide(ctx context.Context, id string, approve bool, by SignInIdentity) error

	// PutFlow, GetFlow and DeleteFlow keep the page's browser flows.
	PutFlow(ctx context.Context, key string, flow ApprovalPageFlow, ttl time.Duration) error
	GetFlow(ctx context.Context, key string) (*ApprovalPageFlow, error)
	DeleteFlow(ctx context.Context, key string) error
}

var (
	approvalStore      ApprovalStore
	approvalStoreMutex sync.RWMutex
)

// RegisterApprovalStore registers the approval store; nil unregisters it.
// muster registers one only when its shared store is on Valkey.
func RegisterApprovalStore(s ApprovalStore) {
	approvalStoreMutex.Lock()
	defer approvalStoreMutex.Unlock()
	approvalStore = s
}

// GetApprovalStore returns the registered approval store, nil without one.
func GetApprovalStore() ApprovalStore {
	approvalStoreMutex.RLock()
	defer approvalStoreMutex.RUnlock()
	return approvalStore
}

type approvalGateKey struct{}

// WithoutApprovalGate marks a context as muster's own call, made on no
// client's behalf, which the effective approval does not ask for (Block
// still holds). Every other dispatch is subject to its target's effective
// approval: the gate is on by default, so a call path added later cannot
// forget it. No production path sets it today; a workflow's steps are
// checked against their run's up-front approval instead.
func WithoutApprovalGate(ctx context.Context) context.Context {
	return context.WithValue(ctx, approvalGateKey{}, true)
}

// ApprovalGateFromContext reports whether the effective approval applies to
// a call made with ctx: always, unless WithoutApprovalGate marked it.
func ApprovalGateFromContext(ctx context.Context) bool {
	skipped, _ := ctx.Value(approvalGateKey{}).(bool)
	return !skipped
}

// PostLoginRedirects is the optional capability of the registered OAuth
// handler that checks a browser return against the operator's
// oauth.mcpClient.postLoginRedirectAllowlist, the one check every muster page
// that sends a browser back to a client uses.
type PostLoginRedirects interface {
	PostLoginRedirectAllowed(raw string) bool
}

// PostLoginRedirectAllowed reports whether raw is an allowed return; false
// without an OAuth handler offering the check (no allowlist, no return).
func PostLoginRedirectAllowed(raw string) bool {
	if checker, ok := GetOAuthHandler().(PostLoginRedirects); ok {
		return checker.PostLoginRedirectAllowed(raw)
	}
	return false
}

// ServerToolLister is the optional capability of the registered aggregator
// that lists a server's tools as the server names them, for the caller: what
// a preset expands from when a connector's approval is set.
type ServerToolLister interface {
	ServerTools(ctx context.Context, server string) ([]mcp.Tool, bool)
}

// ServerTools lists a server's own tools through the registered aggregator;
// false when it cannot.
func ServerTools(ctx context.Context, server string) ([]mcp.Tool, bool) {
	if lister, ok := GetAggregator().(ServerToolLister); ok {
		return lister.ServerTools(ctx, server)
	}
	return nil, false
}
