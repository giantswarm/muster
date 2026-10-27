package credprovider

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// loginIndexHandler is an OAuth handler whose mirror knows which session filed
// each login ID token.
type loginIndexHandler struct {
	api.OAuthHandler
	sessions map[string]string
}

func (h loginIndexHandler) LoginSessionForIDToken(idToken string) string { return h.sessions[idToken] }
func (h loginIndexHandler) ClearLoginIDToken(string, string)             {}

func bindWithBearer(t *testing.T, p *Provider, authorization, contextSession string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, BindingsPath, strings.NewReader(testBindBody))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if contextSession != "" {
		req = req.WithContext(api.WithSessionID(req.Context(), contextSession))
	}
	asPerson("alice", p.HandleBind).ServeHTTP(rec, req)
	return rec
}

// The binding pins the actor to the login of the session the bind request
// came from: the session that filed the bearer as its login ID token, else
// the session muster validated the bearer to.
func TestBindPinsTheLoginSessionOfItsBearer(t *testing.T) {
	api.RegisterOAuthHandler(loginIndexHandler{sessions: map[string]string{"family-token": "family-1", "portal-token": "ext-portal"}})
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	cases := []struct {
		name, bearer, scheme, contextSession, want string
	}{
		{name: "a token muster obtained at its sign-in", bearer: "family-token", contextSession: "ext-of-the-family-token", want: "family-1"},
		{name: "a token a client forwarded", bearer: "portal-token", contextSession: "ext-portal", want: "ext-portal"},
		{name: "muster's own access token", bearer: "muster-access-token", contextSession: "family-2", want: "family-2"},
		{name: "a lowercase scheme", bearer: "family-token", scheme: "bearer ", contextSession: "ext-of-the-family-token", want: "family-1"},
		{name: "a forwarded token muster never filed", bearer: "unknown", contextSession: "ext-unknown", want: ""},
		{name: "no session to pin", bearer: "unknown", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, store := newTestProvider(t, fakeGrants{})
			scheme := tc.scheme
			if scheme == "" {
				scheme = "Bearer "
			}
			rec := bindWithBearer(t, p, scheme+tc.bearer, tc.contextSession)
			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
			b, err := store.Get(t.Context(), testActor)
			require.NoError(t, err)
			require.Equal(t, tc.want, b.LoginSession)
		})
	}
}

func TestActorBearerCarriesThePinnedLoginSession(t *testing.T) {
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	ctx := t.Context()
	iss := newActorIssuer(t)
	store := NewMemoryStore()
	ab := iss.bearer(t, store)

	for _, tc := range []struct{ loginSession, audit string }{
		{"family-1", " login=family"},
		{"ext-portal", " login=forwarded"},
		{"", " login=none"},
	} {
		buf.Reset()
		require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{MusterProvider}, LoginSession: tc.loginSession}, time.Hour))
		caller, err := ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
		require.NoError(t, err)
		require.Equal(t, tc.loginSession, caller.LoginSession)
		require.Contains(t, buf.String(), "details=actor="+testActor+" session=kagent/s1"+tc.audit)
		require.NotContains(t, buf.String(), "family-1", "the audit line names the kind of login, not the session")
	}
}

// The pinned session survives a restart with the binding.
func TestValkeyStoreKeepsTheLoginSession(t *testing.T) {
	srv := miniredis.RunT(t)
	open := func() Store {
		client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
		require.NoError(t, err)
		t.Cleanup(client.Close)
		return NewValkeyStore(client, "muster:", nil)
	}
	require.NoError(t, open().Put(context.Background(), testActor, Binding{Subject: "alice", LoginSession: "family-1"}, time.Hour))
	b, err := open().Get(context.Background(), testActor)
	require.NoError(t, err)
	require.Equal(t, "family-1", b.LoginSession)
}
