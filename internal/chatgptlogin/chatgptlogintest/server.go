package chatgptlogintest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// DeviceAuthID, UserCode, AuthorizationCode and CodeVerifier are what
	// the fake issues for a sign-in.
	DeviceAuthID      = "deviceauth-1"
	UserCode          = "ABCD-1234"
	AuthorizationCode = "authcode-1"
	CodeVerifier      = "verifier-1"
)

// Server is the fake authorization server.
type Server struct {
	*httptest.Server

	// ClientID is the client every request must name.
	ClientID string
	// AccountID is the chatgpt_account_id claim of the ID token.
	AccountID string
	// TokenLifetime is the access token's exp, from its issue. Default: an
	// hour.
	TokenLifetime time.Duration

	mu          sync.Mutex
	disabled    bool
	entered     bool
	codes       int
	polls       int
	exchanges   int
	redemptions int
	generation  int
	refresh     string
}

// New starts the fake for the client.
func New(t testing.TB, clientID string) *Server {
	t.Helper()
	s := &Server{ClientID: clientID, AccountID: "acct-alice", TokenLifetime: time.Hour}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/accounts/deviceauth/usercode", s.userCode)
	mux.HandleFunc("POST /api/accounts/deviceauth/token", s.poll)
	mux.HandleFunc("POST /oauth/token", s.token)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// Disable turns device-code sign-in off: the user-code request answers 404.
func (s *Server) Disable() { s.locked(func() { s.disabled = true }) }

// Enter is the person entering the code at OpenAI.
func (s *Server) Enter() { s.locked(func() { s.entered = true }) }

// Codes is how many user codes were issued.
func (s *Server) Codes() (n int) { s.locked(func() { n = s.codes }); return }

// Polls is how often the sign-in was polled.
func (s *Server) Polls() (n int) { s.locked(func() { n = s.polls }); return }

// Exchanges is how often an authorization code was redeemed.
func (s *Server) Exchanges() (n int) { s.locked(func() { n = s.exchanges }); return }

// Redemptions is how often a refresh token was redeemed successfully.
func (s *Server) Redemptions() (n int) { s.locked(func() { n = s.redemptions }); return }

// RefreshToken is the one refresh token the server honours now.
func (s *Server) RefreshToken() (rt string) { s.locked(func() { rt = s.refresh }); return }

// AccessToken is a JWT access token of the given generation expiring at exp.
func AccessToken(generation int, exp time.Time) string {
	return sign(jwt.MapClaims{"sub": "user-alice", "gen": generation, "exp": exp.Unix()})
}

// IDToken is an ID token carrying the ChatGPT account id as codex reads it.
func IDToken(accountID string) string {
	return sign(jwt.MapClaims{
		"sub":                         "user-alice",
		"email":                       "alice@example.com",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_plan_type": "plus"},
	})
}

func (s *Server) userCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID string `json:"client_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.ClientID != s.ClientID {
		http.Error(w, "bad client", http.StatusBadRequest)
		return
	}
	var disabled bool
	s.locked(func() { disabled = s.disabled })
	if disabled {
		http.NotFound(w, r)
		return
	}
	s.locked(func() { s.codes++ })
	writeJSON(w, map[string]any{"device_auth_id": DeviceAuthID, "user_code": UserCode, "interval": "5"})
}

func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.DeviceAuthID != DeviceAuthID || body.UserCode != UserCode {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}
	var entered bool
	s.locked(func() { s.polls++; entered = s.entered })
	if !entered {
		http.Error(w, `{"error":"authorization_pending"}`, http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{"authorization_code": AuthorizationCode, "code_challenge": "challenge-1", "code_verifier": CodeVerifier})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil || r.PostForm.Get("client_id") != s.ClientID {
		tokenError(w, "invalid_client")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if r.PostForm.Get("code") != AuthorizationCode || r.PostForm.Get("code_verifier") != CodeVerifier ||
			r.PostForm.Get("redirect_uri") != s.URL+"/deviceauth/callback" {
			tokenError(w, "invalid_grant")
			return
		}
		s.exchanges++
	case "refresh_token":
		if s.refresh == "" || r.PostForm.Get("refresh_token") != s.refresh {
			tokenError(w, "invalid_grant")
			return
		}
		s.redemptions++
	default:
		tokenError(w, "unsupported_grant_type")
		return
	}
	s.generation++
	s.refresh = fmt.Sprintf("rt-%d", s.generation)
	// No expires_in: OpenAI's answer carries the lifetime in the JWT only.
	writeJSON(w, map[string]any{
		"access_token":  AccessToken(s.generation, time.Now().Add(s.TokenLifetime)),
		"refresh_token": s.refresh,
		"id_token":      IDToken(s.AccountID),
	})
}

func (s *Server) locked(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func sign(claims jwt.MapClaims) string {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("fake-openai"))
	if err != nil {
		panic(err)
	}
	return token
}
