package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// RequestView is what the decision API shows a trusted client: everything
// the approval page shows, with every text an agent or a connector chose
// made visible the same way (Visible marks bidi, zero-width, blank
// look-alikes and stacked marks as ⟨U+XXXX⟩). A client renders it as text,
// never as markup. Digest is the SHA-256 of the view without it: a decision
// carries it, so a client cannot approve something other than it showed.
type RequestView struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Heading string `json:"heading"`
	Message string `json:"message"`
	// Tool is the action: its title and the server's own (or core, or
	// workflow) name, and the name it is exposed under.
	Tool ViewTool `json:"tool"`
	// Connector is the server's display name and key; empty for core tools
	// and workflows.
	Connector   *ViewConnector         `json:"connector,omitempty"`
	ChangesData bool                   `json:"changesData"`
	Arguments   []api.ApprovalArgument `json:"arguments"`
	Changes     []api.ApprovalChange   `json:"changes,omitempty"`
	Preset      []api.ApprovalArgument `json:"preset,omitempty"`
	Steps       []api.ApprovalStep     `json:"steps,omitempty"`
	// AgentSays is the reason the agent gave: its own words, deciding
	// nothing.
	AgentSays string `json:"agentSays,omitempty"`
	// Agent is the calling agent, where muster knows it.
	Agent *ViewAgent `json:"agent,omitempty"`
	// Why says what made the call ask.
	Why ViewWhy `json:"why"`
	// ActsFor is the person the call acts for.
	ActsFor ViewPerson `json:"actsFor"`
	// CallerKind is "person bearer", "actor JWT" or "delegated".
	CallerKind   string `json:"callerKind"`
	Session      string `json:"session"`
	Client       string `json:"client,omitempty"`
	Installation string `json:"installation,omitempty"`
	// CanonicalArguments and ArgumentsHash are the page's details.
	CanonicalArguments string    `json:"canonicalArguments"`
	ArgumentsHash      string    `json:"argumentsHash"`
	ExpiresAt          time.Time `json:"expiresAt"`
	Digest             string    `json:"digest"`
}

// ViewTool names the action.
type ViewTool struct {
	Title   string `json:"title"`
	Name    string `json:"name"`
	Exposed string `json:"exposed,omitempty"`
	Kind    string `json:"kind"`
}

// ViewConnector names the server.
type ViewConnector struct {
	DisplayName string `json:"displayName"`
	Server      string `json:"server"`
}

// ViewAgent names the calling agent.
type ViewAgent struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	// DisplayName is the name people know the agent by, marked like other
	// agent-controlled text; absent when the binding carries none, then
	// Namespace/Name is the agent's name.
	DisplayName string `json:"displayName,omitempty"`
}

// ViewWhy says why the call asks: the setting and where it came from
// (tool, connector, unreviewed, agent, agentException, governance, builtin,
// unavailable).
type ViewWhy struct {
	Setting string `json:"setting"`
	Source  string `json:"source"`
}

// ViewPerson names the person: their name or email when the token had one,
// and the identity provider's subject and issuer.
type ViewPerson struct {
	Name    string `json:"name,omitempty"`
	Subject string `json:"subject"`
	Issuer  string `json:"issuer"`
}

func visibleArguments(in []api.ApprovalArgument) []api.ApprovalArgument {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.ApprovalArgument, len(in))
	for i, a := range in {
		out[i] = api.ApprovalArgument{Key: Visible(a.Key), Label: Visible(a.Label), Value: Visible(a.Value), Type: a.Type}
	}
	return out
}

// view builds the view of a request.
func (d *DecisionAPI) view(request *api.ApprovalRequest) RequestView {
	return buildView(request, d.Installation)
}

func buildView(request *api.ApprovalRequest, installation string) RequestView {
	shown := request.Display
	v := RequestView{
		ID:          request.ID,
		Status:      request.Status,
		Heading:     heading(request),
		Message:     Visible(shown.Message),
		Tool:        ViewTool{Title: Visible(shown.Title), Name: Visible(request.Target.Name), Exposed: Visible(request.Target.Exposed), Kind: request.Target.Kind},
		ChangesData: shown.ChangesData,
		Arguments:   visibleArguments(shown.Arguments),
		Preset:      visibleArguments(shown.Preset),
		AgentSays:   Visible(shown.AgentSays),
		Why:         ViewWhy{Setting: shown.Setting, Source: shown.Source},
		ActsFor: ViewPerson{Name: Visible(request.RequesterName), Subject: Visible(request.Identity.Subject),
			Issuer: Visible(request.Identity.Issuer)},
		CallerKind:         request.CallerKind,
		Session:            logging.TruncateIdentifier(request.SessionID),
		Client:             Visible(boundClient(request.SessionID)),
		Installation:       installation,
		CanonicalArguments: Visible(request.Arguments),
		ArgumentsHash:      request.ArgumentsHash,
		ExpiresAt:          request.ExpiresAt.UTC(),
	}
	if v.Arguments == nil {
		v.Arguments = []api.ApprovalArgument{}
	}
	if request.Target.Server != "" || shown.Connector != "" {
		v.Connector = &ViewConnector{DisplayName: Visible(shown.Connector), Server: Visible(request.Target.Server)}
	}
	for _, c := range shown.Changes {
		v.Changes = append(v.Changes, api.ApprovalChange{Field: Visible(c.Field), Old: Visible(c.Old), New: Visible(c.New)})
	}
	for _, s := range shown.Steps {
		v.Steps = append(v.Steps, api.ApprovalStep{ID: Visible(s.ID), Label: Visible(s.Label), Tool: Visible(s.Tool), Asks: s.Asks,
			Workflow: Visible(s.Workflow), Arguments: visibleArguments(s.Arguments)})
	}
	if request.Agent != "" {
		namespace, name, ok := strings.Cut(request.Agent, "/")
		if !ok {
			namespace, name = "", request.Agent
		}
		v.Agent = &ViewAgent{Namespace: Visible(namespace), Name: Visible(name), DisplayName: Visible(api.CapAgentDisplayName(request.AgentDisplayName))}
	}
	v.Digest = viewDigest(v)
	return v
}

// viewDigest hashes a view without its status and digest: what the person
// saw, not whether it is still pending.
func viewDigest(v RequestView) string {
	v.Digest, v.Status = "", ""
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// DisplayDigest returns the digest of what the decision API shows for a
// request on an installation.
func DisplayDigest(request *api.ApprovalRequest, installation string) string {
	return buildView(request, installation).Digest
}
