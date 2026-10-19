package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// A server outside muster's namespace signs in with a client of its own
// namespace (agent-permissions D9, Q20-Q22). These tests drive the client with
// the issuer keys the aggregator passes (api.ServerIssuerKey).

const (
	tenantCIMDURL   = "https://muster.example.com/.well-known/oauth-client.json"
	tenantPublicURL = "https://muster.example.com"
)

func newTenantTestClient(t *testing.T) *Client {
	t.Helper()
	client := NewClient(tenantCIMDURL, tenantPublicURL, "/oauth/proxy/callback", "openid")
	t.Cleanup(client.Stop)
	return client
}

func tenantChallenge(issuer string) AuthChallengeParams {
	return AuthChallengeParams{
		SessionID:  "session-1",
		UserID:     "user-1",
		ServerName: "org-a/github",
		Issuer:     issuer,
		Resource:   "https://mcp.example.com/mcp",
		Scope:      "openid",
	}
}

func TestClient_TenantSignIn_UsesOnlyAClientOfItsNamespace(t *testing.T) {
	t.Run("no CIMD client: an AS that offers only CIMD is refused", func(t *testing.T) {
		as := newDCRTestServer(t)
		as.advertiseCIMD = true
		client := newTenantTestClient(t)

		_, _, err := client.GenerateAuthURL(context.Background(), tenantChallenge(api.IssuerKey("org-a", as.server.URL)))
		if err == nil || !strings.Contains(err.Error(), "own pin") {
			t.Fatalf("GenerateAuthURL = %v, want the refusal of muster's CIMD client", err)
		}
	})

	t.Run("a registration of its own, listing only its namespace's redirect URI", func(t *testing.T) {
		as := newDCRTestServer(t)
		as.advertiseCIMD = true
		as.advertiseDCR = true
		client := newTenantTestClient(t)
		nsRedirect := tenantPublicURL + "/oauth/ns/org-a/callback"

		startURL, resolved, err := client.GenerateAuthURL(context.Background(), tenantChallenge(api.IssuerKey("org-a", as.server.URL)))
		if err != nil {
			t.Fatalf("GenerateAuthURL: %v", err)
		}
		if resolved.Method != ClientIDMethodDCR {
			t.Errorf("method = %q, want %q", resolved.Method, ClientIDMethodDCR)
		}
		query := upstreamAuthQuery(t, client, startURL)
		if got := query.Get("redirect_uri"); got != nsRedirect {
			t.Errorf("redirect_uri = %q, want %q", got, nsRedirect)
		}
		registration, _ := as.lastRegistration.Load().(*pkgoauth.ClientMetadata)
		if registration == nil || len(registration.RedirectURIs) != 1 || registration.RedirectURIs[0] != nsRedirect {
			t.Errorf("registration redirect_uris = %+v, want only %q", registration, nsRedirect)
		}

		// muster's namespace keeps its CIMD client against the same AS ...
		startURL, resolved, err = client.GenerateAuthURL(context.Background(), tenantChallenge(as.server.URL))
		if err != nil || resolved.Method != ClientIDMethodCIMD {
			t.Fatalf("muster's namespace: method %v, err %v; want the CIMD client", resolved, err)
		}
		if got := upstreamAuthQuery(t, client, startURL).Get("redirect_uri"); got != tenantPublicURL+"/oauth/proxy/callback" {
			t.Errorf("muster's namespace redirect_uri = %q, want the plain callback", got)
		}
		// ... and another namespace registers a client of its own.
		if _, _, err := client.GenerateAuthURL(context.Background(), tenantChallenge(api.IssuerKey("org-b", as.server.URL))); err != nil {
			t.Fatalf("org-b: %v", err)
		}
		if n := as.registrationCount.Load(); n != 2 {
			t.Errorf("registrations = %d, want one per namespace", n)
		}
	})

	t.Run("a pin of muster's namespace is not lent to a tenant", func(t *testing.T) {
		client := newTenantTestClient(t)
		// No AS answers at this issuer: only the pinned metadata could carry a flow.
		const issuer = "https://github.invalid/login/oauth"
		client.PinIssuer(issuer, IssuerPin{ClientID: "platform-app", ClientSecret: "s"},
			&pkgoauth.Metadata{AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token"})

		if _, _, err := client.GenerateAuthURL(context.Background(), tenantChallenge(api.IssuerKey("org-a", issuer))); err == nil {
			t.Fatal("a tenant flow must not run on the pin of muster's namespace")
		}
		if id, _ := client.GetClientCredentialsForIssuer(context.Background(), api.IssuerKey("org-a", issuer)); id != "" {
			t.Errorf("refresh client for the tenant = %q, want none", id)
		}
	})

	t.Run("a tenant pin applies to its namespace only", func(t *testing.T) {
		client := newTenantTestClient(t)
		const issuer = "https://app.hubspot.invalid"
		tenantKey := api.IssuerKey("org-a", issuer)
		client.PinIssuer(tenantKey, IssuerPin{ClientID: "hubspot-connector", ClientSecret: "s"},
			&pkgoauth.Metadata{Issuer: issuer, AuthorizationEndpoint: issuer + "/oauth/authorize", TokenEndpoint: issuer + "/oauth/v1/token"})

		startURL, resolved, err := client.GenerateAuthURL(context.Background(), tenantChallenge(tenantKey))
		if err != nil {
			t.Fatalf("GenerateAuthURL with the tenant's pin: %v", err)
		}
		if resolved.ClientID != "hubspot-connector" {
			t.Errorf("client = %q, want the tenant's pinned client", resolved.ClientID)
		}
		query := upstreamAuthQuery(t, client, startURL)
		if got := query.Get("redirect_uri"); got != tenantPublicURL+"/oauth/ns/org-a/callback" {
			t.Errorf("redirect_uri = %q, want the namespace's callback", got)
		}

		for _, other := range []string{issuer, api.IssuerKey("org-b", issuer)} {
			if _, _, err := client.GenerateAuthURL(context.Background(), tenantChallenge(other)); err == nil {
				t.Errorf("flow for %q ran on org-a's pin", other)
			}
		}
	})

	t.Run("a tenant pin naming muster's CIMD URL as its client is refused", func(t *testing.T) {
		client := newTenantTestClient(t)
		const issuer = "https://idp.invalid"
		tenantKey := api.IssuerKey("org-a", issuer)
		client.PinIssuer(tenantKey, IssuerPin{ClientID: tenantCIMDURL},
			&pkgoauth.Metadata{AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token"})

		_, _, err := client.GenerateAuthURL(context.Background(), tenantChallenge(tenantKey))
		if err == nil || !strings.Contains(err.Error(), "client ID metadata document") {
			t.Fatalf("GenerateAuthURL = %v, want the refusal of the CIMD URL as a tenant client", err)
		}
	})
}

// The callback accepts a flow only on the redirect URI of the flow's
// namespace, and checks it before the code reaches any token endpoint (Q23,
// N11, N12, N17).
func TestHandler_HandleCallback_FlowOnlyOnItsNamespaceRedirectURI(t *testing.T) {
	tests := []struct {
		name      string
		namespace string // of the flow; "" is muster's namespace
		path      string
		accepted  bool
	}{
		{name: "tenant flow on its namespace's callback", namespace: "org-a", path: "/oauth/ns/org-a/callback", accepted: true},
		{name: "muster's flow on the plain callback", path: "/oauth/proxy/callback", accepted: true},
		{name: "tenant flow on the plain callback", namespace: "org-a", path: "/oauth/proxy/callback"},
		{name: "tenant flow on another tenant's callback", namespace: "org-a", path: "/oauth/ns/org-b/callback"},
		{name: "muster's flow on a tenant's callback", path: "/oauth/ns/org-a/callback"},
		{name: "tenant flow on an encoded spelling of its callback", namespace: "org-a", path: "/oauth/ns/org%2Da/callback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenForm := make(chan url.Values, 1)
			authServer := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
			defer authServer.Close()
			client := NewClient("client-id", tenantPublicURL, "/oauth/proxy/callback", "openid")
			defer client.Stop()

			issuerKey := api.IssuerKey(tc.namespace, authServer.URL)
			client.PinIssuer(issuerKey, IssuerPin{ClientID: "own-client"}, nil)
			encodedState := storeCallbackState(t, client, issuerKey)

			query := url.Values{"code": {"auth-code"}, "state": {encodedState}, "iss": {authServer.URL}}
			recorder := httptest.NewRecorder()
			NewHandler(client).HandleCallback(recorder,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path+"?"+query.Encode(), nil))

			if !tc.accepted {
				if recorder.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
				}
				select {
				case <-tokenForm:
					t.Error("the token endpoint received a code that arrived on another namespace's redirect URI")
				default:
				}
				return
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %q)", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			form := <-tokenForm
			if got, want := form.Get("redirect_uri"), tenantPublicURL+tc.path; got != want {
				t.Errorf("token request redirect_uri = %q, want %q", got, want)
			}
			if got := form.Get("client_id"); got != "own-client" {
				t.Errorf("token request client_id = %q, want the namespace's own client", got)
			}
			if token := client.GetToken("session-1", issuerKey, ""); token == nil {
				t.Error("the token is not filed under the flow's issuer key")
			}
		})
	}
}

// A tenant's issuer key never reaches the login ID token muster forwards for
// its own SSO, even when the tenant pins muster's own issuer (N4), and a
// tenant's subject grant is never released by the token broker (N5).
func TestAdapter_TenantKeyReachesNoTokenOfMustersNamespace(t *testing.T) {
	manager := NewManager(config.OAuthMCPClientConfig{
		Enabled: true, PublicURL: tenantPublicURL, ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
	})
	defer manager.Stop()
	adapter := NewAdapter(manager)
	const dex = "https://dex.example.com"
	tenantDex := api.IssuerKey("org-a", dex)
	exp := time.Now().Add(time.Hour)

	api.StoreLoginIDToken(adapter, "sess", "user", dex, "login-id-token", exp)
	if got := adapter.GetFullTokenByIssuer("sess", tenantDex); got != nil {
		t.Errorf("a tenant pin with Dex's issuer found %+v, want no token", got)
	}

	// A session whose only ID token came from a tenant's sign-in has no
	// muster login to fall back to.
	manager.StoreToken("other-sess", "user", tenantDex, &pkgoauth.Token{AccessToken: "at", IDToken: "tenant-id-token", ExpiresAt: exp, Issuer: tenantDex})
	if got := adapter.FindTokenWithIDToken("other-sess"); got != nil {
		t.Errorf("FindTokenWithIDToken = %+v, want none for a tenant's token", got)
	}

	const github = "https://github.com/login/oauth"
	tenantGitHub := api.IssuerKey("org-a", github)
	adapter.PinIssuer(tenantGitHub, api.IssuerPin{SubjectScoped: true})
	manager.client.StoreToken("sess", "user", &pkgoauth.Token{AccessToken: "tenant-grant", ExpiresAt: exp, Issuer: tenantGitHub})
	if got := adapter.SubjectGrantForRelease(context.Background(), "user", github); got != nil {
		t.Errorf("the broker's plain issuer released %+v, want no tenant grant", got)
	}
	if got := adapter.SubjectGrantForRelease(context.Background(), "user", tenantGitHub); got == nil || got.AccessToken != "tenant-grant" {
		t.Errorf("the tenant's own key = %+v, want its grant (it is filed, only never released to a plain target)", got)
	}
}
