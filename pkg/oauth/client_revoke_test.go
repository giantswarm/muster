package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClient_RevokeToken(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("request = %s %s, want a form POST", r.Method, r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		form = r.PostForm
	}))
	defer srv.Close()

	err := NewClient().RevokeToken(context.Background(), srv.URL, "rt-1", "refresh_token", "app", "secret")
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	for field, want := range map[string]string{"token": "rt-1", "token_type_hint": "refresh_token", "client_id": "app", "client_secret": "secret"} {
		if got := form.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
}

func TestClient_RevokeToken_PublicClientSendsNoSecret(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
	}))
	defer srv.Close()

	if err := NewClient().RevokeToken(context.Background(), srv.URL, "at-1", "access_token", "https://muster.example.com/cimd", ""); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, ok := form["client_secret"]; ok {
		t.Error("a public client must not send client_secret")
	}
}

func TestClient_RevokeToken_RefusalCarriesTheStatusOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"unauthorized_client","token":"rt-secret"}`))
	}))
	defer srv.Close()

	err := NewClient().RevokeToken(context.Background(), srv.URL, "rt-secret", "refresh_token", "app", "secret")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("RevokeToken = %v, want an error naming status 403", err)
	}
	if strings.Contains(err.Error(), "rt-secret") {
		t.Errorf("the error echoes the token: %v", err)
	}
}
