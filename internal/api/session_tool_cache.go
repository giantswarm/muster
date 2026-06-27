package api

import (
	"context"
	"sync"
)

// SessionToolMemo caches a session's resolved accessible-tool set for the
// duration of a single request, so callers that perform many per-item
// availability checks (e.g. listing N workflows) rebuild the session tool set
// at most once instead of once per item.
//
// The accessible-tool set is identical for every item in one list request, yet
// resolving it is expensive (a full rebuild across all backend MCP servers).
// Without the memo, a workflow list of N entries rebuilds it N times — the
// O(workflows) blow-up that made /api/muster/workflows take ~30 s.
type SessionToolMemo struct {
	mu    sync.Mutex
	tools map[string]struct{}
	done  bool
}

// sessionToolMemoContextKey is the context key under which a SessionToolMemo is
// carried for the lifetime of a request.
type sessionToolMemoContextKey struct{}

// WithSessionToolMemo returns a context carrying an empty SessionToolMemo. It is
// idempotent: if the context already carries a memo, the same context is
// returned unchanged so nested scopes share one cache.
func WithSessionToolMemo(ctx context.Context) context.Context {
	if SessionToolMemoFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, sessionToolMemoContextKey{}, &SessionToolMemo{})
}

// SessionToolMemoFromContext returns the SessionToolMemo carried by ctx, or nil
// when none is present. Callers outside a memo-scoped request get nil and keep
// their per-call behavior.
func SessionToolMemoFromContext(ctx context.Context) *SessionToolMemo {
	memo, _ := ctx.Value(sessionToolMemoContextKey{}).(*SessionToolMemo)
	return memo
}

// Resolve returns the cached session tool set, invoking build exactly once to
// populate it on first use. Subsequent calls return the cached set without
// calling build again. Safe for concurrent use.
func (m *SessionToolMemo) Resolve(build func() map[string]struct{}) map[string]struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.done {
		m.tools = build()
		m.done = true
	}
	return m.tools
}
