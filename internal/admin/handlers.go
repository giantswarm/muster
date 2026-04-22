package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// handleList renders the session list (HTML) or returns JSON when the client
// asks for it via Accept.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.deps.ListSessions(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("list sessions: %v", err), http.StatusInternalServerError)
		return
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Subject != sessions[j].Subject {
			return sessions[i].Subject < sessions[j].Subject
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, sessions)
		return
	}

	data := struct {
		Title    string
		Now      time.Time
		Sessions []SessionSummary
	}{
		Title:    "Sessions",
		Now:      time.Now(),
		Sessions: sessions,
	}
	s.render(w, "list.html.tmpl", data)
}

// handleDetail renders the detail view for a session. JWTs are decoded here
// (not by the aggregator) so all decode/redaction logic lives in one place.
func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	detail, ok, err := s.deps.GetSessionDetail(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("get session: %v", err), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	decoded := make([]*DecodedJWT, 0, len(detail.Tokens))
	for _, tok := range detail.Tokens {
		decoded = append(decoded, DecodeJWT(tok.Label, tok.Raw))
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, struct {
			*SessionDetail
			DecodedTokens []*DecodedJWT `json:"decodedTokens"`
		}{SessionDetail: detail, DecodedTokens: decoded})
		return
	}

	data := struct {
		Title  string
		Now    time.Time
		Detail *SessionDetail
		Tokens []*DecodedJWT
	}{
		Title:  "Session " + shortID(detail.SessionID),
		Now:    time.Now(),
		Detail: detail,
		Tokens: decoded,
	}
	s.render(w, "detail.html.tmpl", data)
}

// handleDelete performs a full session teardown and redirects back to /sessions.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	if err := s.deps.DeleteSession(r.Context(), id); err != nil {
		http.Error(w, fmt.Sprintf("delete session: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sessions", http.StatusSeeOther)
}

// handleDisconnect performs a per-server logout and redirects back to the
// session detail view.
func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	if id == "" || name == "" {
		http.Error(w, "missing session id or server name", http.StatusBadRequest)
		return
	}
	if err := s.deps.DisconnectServer(r.Context(), id, name); err != nil {
		http.Error(w, fmt.Sprintf("disconnect: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sessions/"+id, http.StatusSeeOther)
}

// render executes the named view. We first render to a buffer so that any
// template error produces a clean 500 instead of a half-written page + stray
// WriteHeader call.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t, ok := s.tmpl[name]
	if !ok {
		http.Error(w, "render: unknown template "+name, http.StatusInternalServerError)
		return
	}
	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, fmt.Sprintf("render: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, buf.String())
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(body); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		// Connection probably closed — nothing useful to do here.
		return
	}
}

// shortID truncates a session ID for display without leaking the full value
// in titles. The detail page header shows the full ID; titles use the prefix.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

// templateFuncs are exposed to the HTML templates.
var templateFuncs = template.FuncMap{
	"shortID": shortID,
	"isoTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	},
	"humanTime":  humanTime,
	"humanUntil": humanUntil,
}

// humanTime formats a time as "X ago" for the list view.
func humanTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// humanUntil formats a time as "in X" for token expiry.
func humanUntil(t time.Time) string {
	if t.IsZero() {
		return "no expiry"
	}
	d := time.Until(t)
	if d < 0 {
		return fmt.Sprintf("expired %s ago", humanTimeAbs(-d))
	}
	return "in " + humanTimeAbs(d)
}

func humanTimeAbs(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
