package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_RefreshToken(t *testing.T) {
	var gotForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = map[string]string{}
		for k := range r.PostForm {
			gotForm[k] = r.PostForm.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ghu_new", "token_type": "bearer", "refresh_token": "ghr_new", "expires_in": 28800,
		})
	}))
	defer srv.Close()

	client := NewClient(WithHTTPClient(srv.Client()))
	token, err := client.RefreshToken(context.Background(), srv.URL+"/token", "ghr_old", "Iv23li", "s3cr3t")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if token.AccessToken != "ghu_new" || token.RefreshToken != "ghr_new" || token.ExpiresIn != 28800 || token.ExpiresAt.IsZero() {
		t.Errorf("token = %+v", token)
	}
	if gotForm["grant_type"] != "refresh_token" || gotForm["refresh_token"] != "ghr_old" || gotForm["client_id"] != "Iv23li" || gotForm["client_secret"] != "s3cr3t" {
		t.Errorf("form = %v", gotForm)
	}
}

func TestClient_RefreshToken_GitHubErrorWithSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// GitHub: HTTP 200 with an error object.
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "bad_refresh_token", "error_description": "The refresh token passed is incorrect or expired.",
		})
	}))
	defer srv.Close()

	client := NewClient(WithHTTPClient(srv.Client()))
	_, err := client.RefreshToken(context.Background(), srv.URL+"/token", "ghr_dead", "Iv23li", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.Code != ErrBadRefreshToken || tokenErr.StatusCode != http.StatusOK {
		t.Errorf("err = %#v, want a TokenEndpointError with bad_refresh_token and status 200", err)
	}
	if !IsRefreshTokenRejected(err) {
		t.Error("bad_refresh_token is a rejected refresh token")
	}
}

func TestIsRefreshTokenRejected(t *testing.T) {
	if !IsRefreshTokenRejected(&TokenEndpointError{StatusCode: 400, Code: ErrInvalidGrant}) {
		t.Error("invalid_grant is a rejection")
	}
	if IsRefreshTokenRejected(&TokenEndpointError{StatusCode: 401, Code: ErrInvalidClient}) {
		t.Error("invalid_client says nothing about the refresh token")
	}
	if IsRefreshTokenRejected(&TokenEndpointError{StatusCode: 503}) {
		t.Error("a 5xx without an error object is transient")
	}
	if IsRefreshTokenRejected(errors.New("dial tcp: connection refused")) {
		t.Error("a transport error is not a rejection")
	}
}
