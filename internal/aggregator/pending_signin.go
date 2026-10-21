package aggregator

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/giantswarm/muster/v5/internal/api"
)

// pendingSignInTTL is how long an auth_required answer reuses the sign-in
// challenge it issued. The challenge's OAuth state lives 10 minutes from its
// creation (the state stores' expiry): a link handed out at the end of the
// reuse window still leaves the person 5 minutes to finish signing in.
const pendingSignInTTL = 5 * time.Minute

// pendingSignIns holds the sign-in challenge each session awaits per server,
// so the auth_required answer of a gated tool call reuses it instead of
// running a fresh core_auth_login: a client polling the tool spends one of the
// person's sign-in attempts, not one per call. A challenge is forgotten when
// it expires, when the sign-in completes and when the session loses or drops
// its authentication to the server, so the next answer issues a new one.
//
// The zero value is ready to use. It is local to the replica: a session whose
// calls reach several replicas holds at most one challenge on each.
type pendingSignIns struct {
	mu      sync.Mutex
	entries map[pendingSignInKey]pendingSignIn
	// starts lets concurrent answers of one session and server share the
	// login that issues their challenge.
	starts singleflight.Group
	// now is the clock; nil means time.Now. Tests set it.
	now func() time.Time
}

type pendingSignInKey struct {
	sessionID string
	server    string
}

type pendingSignIn struct {
	login     *api.CallToolResult
	expiresAt time.Time
}

func (p *pendingSignIns) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// answer returns the session's pending challenge for the server, or runs
// login and keeps its answer when it is a new challenge. A login that connects
// the session, or refuses the sign-in, is returned and not kept.
func (p *pendingSignIns) answer(sessionID, server string, login func() (*api.CallToolResult, error)) (*api.CallToolResult, error) {
	key := pendingSignInKey{sessionID: sessionID, server: server}
	if pending, ok := p.get(key); ok {
		return pending, nil
	}
	result, err, _ := p.starts.Do(sessionID+"\x00"+server, func() (any, error) {
		if pending, ok := p.get(key); ok {
			return pending, nil
		}
		result, err := login()
		if err == nil && isChallenge(result) {
			p.put(key, result)
		}
		return result, err
	})
	if err != nil {
		return nil, err
	}
	return result.(*api.CallToolResult), nil
}

// forget drops the session's pending challenge for the server.
func (p *pendingSignIns) forget(sessionID, server string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, pendingSignInKey{sessionID: sessionID, server: server})
}

func (p *pendingSignIns) get(key pendingSignInKey) (*api.CallToolResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[key]
	if !ok {
		return nil, false
	}
	if !p.clock().Before(entry.expiresAt) {
		delete(p.entries, key)
		return nil, false
	}
	return entry.login, true
}

func (p *pendingSignIns) put(key pendingSignInKey, login *api.CallToolResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	if p.entries == nil {
		p.entries = make(map[pendingSignInKey]pendingSignIn)
	}
	for k, entry := range p.entries {
		if !now.Before(entry.expiresAt) {
			delete(p.entries, k)
		}
	}
	p.entries[key] = pendingSignIn{login: login, expiresAt: now.Add(pendingSignInTTL)}
}

// isChallenge reports whether a core_auth_login answer is a sign-in challenge:
// a link for the person to follow, not a connection or a refusal.
func isChallenge(login *api.CallToolResult) bool {
	if login == nil || login.IsError {
		return false
	}
	fields, ok := login.StructuredContent.(map[string]any)
	if !ok {
		return false
	}
	authURL, _ := fields["authUrl"].(string)
	return authURL != ""
}
