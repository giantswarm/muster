package credprovider

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// BindingsPath is the HTTP path of the Session binding endpoints.
const BindingsPath = "/session-bindings"

// maxBindRequestBytes bounds a bind request body.
const maxBindRequestBytes = 16 << 10

// BindRequest is the body of PUT /session-bindings.
type BindRequest struct {
	// Actor is the Session's sandbox actor SPIFFE id.
	Actor string `json:"actor"`
	// Session names the Session (<namespace>/<name>), for the audit log.
	Session string `json:"session"`
	// Providers are the provider instances the Session may use.
	Providers []string `json:"providers"`
}

// HandleBind is PUT /session-bindings: binds the actor to the calling person.
// The caller must be authenticated (api.GetSubjectFromContext non-empty).
func (p *Provider) HandleBind(w http.ResponseWriter, r *http.Request) {
	sub := api.GetSubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "unauthorized: no person identity", http.StatusUnauthorized)
		return
	}
	var req BindRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBindRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.Actor, actorSPIFFEPrefix) || validateSPIFFE(req.Actor) != nil {
		http.Error(w, "bad request: actor must be a Substrate actor SPIFFE id ("+actorSPIFFEPrefix+"<atespace>/<actor>)", http.StatusBadRequest)
		return
	}
	if req.Session == "" {
		http.Error(w, "bad request: session is required", http.StatusBadRequest)
		return
	}
	if len(req.Providers) == 0 {
		http.Error(w, "bad request: providers is empty", http.StatusBadRequest)
		return
	}
	for _, provider := range req.Providers {
		if provider == MusterProvider && p.musterBearer {
			continue
		}
		if _, ok := p.releasable[provider]; !ok {
			http.Error(w, "bad request: provider "+provider+" is not releasable; releasable: "+strings.Join(p.releasableNames(), ", "), http.StatusBadRequest)
			return
		}
	}

	b := Binding{Subject: sub, Session: req.Session, Providers: req.Providers, BoundAt: time.Now().UTC()}
	err := p.store.Put(r.Context(), req.Actor, b, p.ttl)
	if errors.Is(err, ErrBoundToAnother) {
		logging.WarnWithAttrs(logSubsystem, "bind_refused",
			slog.String("reason", err.Error()), slog.String("sub", sub),
			slog.String("actor", req.Actor), slog.String("session", req.Session))
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "bind_failed", slog.String("actor", req.Actor), slog.String("error", err.Error()))
		http.Error(w, "binding store unavailable", http.StatusServiceUnavailable)
		return
	}
	logging.InfoWithAttrs(logSubsystem, "session_bound",
		slog.String("sub", sub), slog.String("session", req.Session),
		slog.String("actor", req.Actor), slog.Any("providers", req.Providers),
		slog.Duration("ttl", p.ttl))
	w.WriteHeader(http.StatusNoContent)
}

// HandleUnbind is DELETE /session-bindings?actor=<id>: removes the calling
// person's binding of the actor. Unbinding an unbound actor succeeds.
func (p *Provider) HandleUnbind(w http.ResponseWriter, r *http.Request) {
	sub := api.GetSubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "unauthorized: no person identity", http.StatusUnauthorized)
		return
	}
	actor := r.URL.Query().Get("actor")
	if actor == "" {
		http.Error(w, "bad request: actor is required", http.StatusBadRequest)
		return
	}
	err := p.store.Delete(r.Context(), actor, sub)
	if errors.Is(err, ErrBoundToAnother) {
		logging.WarnWithAttrs(logSubsystem, "unbind_refused",
			slog.String("reason", err.Error()), slog.String("sub", sub), slog.String("actor", actor))
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "unbind_failed", slog.String("actor", actor), slog.String("error", err.Error()))
		http.Error(w, "binding store unavailable", http.StatusServiceUnavailable)
		return
	}
	logging.InfoWithAttrs(logSubsystem, "session_unbound", slog.String("sub", sub), slog.String("actor", actor))
	w.WriteHeader(http.StatusNoContent)
}

func (p *Provider) releasableNames() []string {
	names := make([]string, 0, len(p.releasable)+1)
	for name := range p.releasable {
		names = append(names, name)
	}
	if p.musterBearer {
		names = append(names, MusterProvider)
	}
	sort.Strings(names)
	return names
}
