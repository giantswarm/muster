package oauth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

// refreshRequestForm returns the form of a refresh_token grant request, the
// only token request the muster processes sharing a token file must
// coordinate. Its body is read; the caller sends the request with the form
// re-encoded (withForm).
func refreshRequestForm(req *http.Request) (url.Values, bool) {
	if req.Method != http.MethodPost || req.Body == nil ||
		!strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return nil, false
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || form.Get("grant_type") != "refresh_token" {
		return nil, false
	}
	return form, true
}

// withForm returns a copy of req carrying form as its body.
func withForm(req *http.Request, form url.Values) *http.Request {
	encoded := form.Encode()
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(strings.NewReader(encoded))
	out.ContentLength = int64(len(encoded))
	out.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(encoded)), nil
	}
	return out
}

// coordinatedRefresh sends a refresh only while holding the token file's
// refresh lock, and only with the refresh token the file holds now. Several
// muster processes (agents, CLI calls) share one token file, each with its
// own cached copy and its own expiry clock, so they reach a refresh at the
// same moment or with a refresh token another process has already spent; the
// server takes the second use for a stolen token and revokes the person's
// whole grant. Under the lock:
//
//   - the file holds a different, still valid token: another process
//     refreshed already, and the request is answered with that token without
//     reaching the server;
//   - the file holds a different refresh token, its access token expired: the
//     request goes out with the file's refresh token;
//   - otherwise the request goes out as it is, and a successful response is
//     written to the file before the lock is released, so the next process
//     finds it.
func (r idTokenRecorder) coordinatedRefresh(req *http.Request, form url.Values) (*http.Response, error) {
	tokens := r.store.tokenStore
	unlock, err := tokens.LockRefresh(r.store.serverURL)
	if err != nil {
		return nil, err
	}
	defer unlock()

	sent := form.Get("refresh_token")
	if current := tokens.ReloadToken(r.store.serverURL); current != nil && current.RefreshToken != "" && current.RefreshToken != sent {
		if current.AccessToken != "" && tokens.isTokenValid(current) {
			return r.adoptedResponse(req, current), nil
		}
		form.Set("refresh_token", current.RefreshToken)
		sent = current.RefreshToken
	}

	resp, body, err := r.send(withForm(req, form))
	if err != nil || body == nil {
		return resp, err
	}

	var refreshed transport.Token
	if json.Unmarshal(body, &refreshed) != nil || refreshed.AccessToken == "" {
		return resp, nil
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = sent
	}
	if refreshed.ExpiresIn > 0 {
		refreshed.ExpiresAt = time.Now().Add(time.Duration(refreshed.ExpiresIn) * time.Second)
	}
	if err := r.store.SaveToken(req.Context(), &refreshed); err != nil {
		return nil, err
	}
	return resp, nil
}

// adoptedResponse answers a refresh with the token another process stored:
// the token endpoint response mcp-go would have read had this process sent
// the refresh itself.
func (r idTokenRecorder) adoptedResponse(req *http.Request, token *StoredToken) *http.Response {
	payload := map[string]any{
		"access_token":  token.AccessToken,
		"token_type":    token.TokenType,
		"refresh_token": token.RefreshToken,
	}
	if !token.Expiry.IsZero() {
		payload["expires_in"] = int64(time.Until(token.Expiry).Seconds())
	}
	if token.IDToken != "" {
		payload["id_token"] = token.IDToken
		r.store.mu.Lock()
		r.store.refreshedIDToken = token.IDToken
		r.store.mu.Unlock()
	}
	body, _ := json.Marshal(payload)

	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}
