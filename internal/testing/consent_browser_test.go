package testing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers/mock"
	"github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/stretchr/testify/require"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

func TestApproveClientConsentCompletesBrowserFlow(t *testing.T) {
	mux := http.NewServeMux()
	transport := httptest.NewServer(mux)
	t.Cleanup(transport.Close)
	authorizationServer, err := server.NewWithCombined(mock.NewProvider(), memory.New(), &server.Config{
		Issuer: transport.URL, AllowLocalhostRedirectURIs: true, DisableNonceEchoRequirement: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, authorizationServer.Shutdown(context.WithoutCancel(t.Context()))) })
	handlers := handler.New(authorizationServer, nil)
	mux.HandleFunc("/oauth/authorize", handlers.ServeAuthorization)
	mux.HandleFunc("/oauth/callback", handlers.ServeCallback)
	mux.HandleFunc("/oauth/token", handlers.ServeToken)
	const redirectURI = "http://127.0.0.1:54650/callback"
	client, _, err := authorizationServer.RegisterClient(t.Context(), "browser", "public", "none", []string{redirectURI}, nil, "192.0.2.1", 10)
	require.NoError(t, err)
	pkce, err := pkgoauth.GeneratePKCE()
	require.NoError(t, err)
	query := url.Values{
		"client_id": {client.ClientID}, "redirect_uri": {redirectURI}, "response_type": {"code"},
		"state":          {"browser-consent-state-with-enough-entropy"},
		"code_challenge": {pkce.CodeChallenge}, "code_challenge_method": {pkce.CodeChallengeMethod},
	}
	browser, err := newBrowser()
	require.NoError(t, err)
	upstream, err := approveClientConsent(t.Context(), browser, transport.URL+"/oauth/authorize?"+query.Encode())
	require.NoError(t, err)
	providerURL, err := url.Parse(upstream)
	require.NoError(t, err)
	callback := transport.URL + "/oauth/callback?code=provider-code&state=" + url.QueryEscape(providerURL.Query().Get("state"))
	location, err := redirectLocation(t.Context(), browser, callback)
	require.NoError(t, err)
	redirect, err := url.Parse(location)
	require.NoError(t, err)
	require.Equal(t, query.Get("state"), redirect.Query().Get("state"))
	tokens, err := exchangeOAuthToken(t.Context(), transport.URL+"/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")},
		"redirect_uri": {redirectURI}, "client_id": {client.ClientID}, "code_verifier": {pkce.CodeVerifier},
	})
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessToken)
}

func TestApproveClientConsentRejectsOtherForms(t *testing.T) {
	for _, form := range []string{
		`<form method="post" action="https://attacker.example/oauth/authorize"><input name="consent_id" value="id"></form>`,
		`<form method="post" action="/login"><input name="password" value=""></form>`,
	} {
		t.Run(form, func(t *testing.T) {
			posts := 0
			transport := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
				}
				_, _ = w.Write([]byte(form))
			}))
			t.Cleanup(transport.Close)
			browser, err := newBrowser()
			require.NoError(t, err)
			_, err = approveClientConsent(t.Context(), browser, transport.URL+"/oauth/authorize")
			require.Error(t, err)
			require.Zero(t, posts)
		})
	}
}
