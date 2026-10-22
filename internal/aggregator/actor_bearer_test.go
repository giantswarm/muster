package aggregator

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

const testActorIssuer = "https://idp.substrate.svc"

// fakeActorJWT carries iss and sub; the test verifier trusts it.
func fakeActorJWT(iss, sub string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"ES256"}`)) + "." + enc([]byte(`{"iss":"`+iss+`","sub":"`+sub+`"}`)) + ".sig"
}

func testActorBearer(t *testing.T, store credprovider.Store) *credprovider.ActorBearer {
	t.Helper()
	verify := func(_ context.Context, token string) (credprovider.ActorToken, error) {
		if token == fakeActorJWT(testActorIssuer, "actor/kagent/forged") {
			return credprovider.ActorToken{}, errors.New("signature invalid")
		}
		return credprovider.ActorToken{Subject: "actor/kagent/session-a", ActorUID: "uid-a", Expiry: time.Now().Add(time.Minute)}, nil
	}
	ab, err := credprovider.NewActorBearer(credprovider.ActorBearerOptions{Issuer: testActorIssuer, Verify: verify, Store: store})
	require.NoError(t, err)
	return ab
}

func TestActorBearerHandler(t *testing.T) {
	ctx := context.Background()
	store := credprovider.NewMemoryStore()
	ab := testActorBearer(t, store)

	var served struct{ sub, session, bearer, idToken string }
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.sub = api.GetSubjectFromContext(r.Context())
		served.session = api.GetSessionIDFromContext(r.Context())
		served.bearer = server.CallerTokensFromContext(r.Context()).Bearer
		served.idToken = server.CallerTokensFromContext(r.Context()).IDToken
		w.WriteHeader(http.StatusOK)
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	var authenticated, authenticatedIDToken string
	withPersonToken := func(ctx context.Context, caller *credprovider.ActorCaller) context.Context {
		return server.ContextWithIDToken(ctx, "id-token-of-"+caller.Subject)
	}
	h := actorBearerHandler(ab, mcp, next, func(ctx context.Context, sessionID string) {
		authenticated = sessionID
		authenticatedIDToken, _ = server.GetIDTokenFromContext(ctx)
	}, withPersonToken)

	call := func(bearer string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	token := fakeActorJWT(testActorIssuer, "actor/kagent/session-a")

	assert.Equal(t, http.StatusTeapot, call(""), "no bearer goes to the OAuth chain")
	assert.Equal(t, http.StatusTeapot, call("opaque-muster-token"), "a muster token goes to the OAuth chain")
	assert.Equal(t, http.StatusTeapot, call(fakeActorJWT("https://dex.example.com", "alice")), "a Dex token goes to the OAuth chain")
	assert.Equal(t, http.StatusForbidden, call(token), "an unbound actor is refused")
	assert.Equal(t, http.StatusUnauthorized, call(fakeActorJWT(testActorIssuer, "actor/kagent/forged")))

	require.NoError(t, store.Put(ctx, "spiffe://substrate-actor.local/actor/kagent/session-a",
		credprovider.Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{credprovider.MusterProvider}}, time.Hour))
	require.Equal(t, http.StatusOK, call(token))
	assert.Equal(t, "alice", served.sub)
	assert.NotEmpty(t, served.session)
	assert.Equal(t, served.session, authenticated)
	assert.Empty(t, served.bearer, "the actor JWT is never forwarded downstream")
	assert.Equal(t, "id-token-of-alice", served.idToken, "the bound person's ID token is what servers forward or exchange")
	assert.Equal(t, "id-token-of-alice", authenticatedIDToken, "the session's servers connect with it")
}

// The binding's toolset reaches the meta-tools beside the header the request
// sends, through the transport's per-request context.
func TestActorBearerHandlerBindsTheToolset(t *testing.T) {
	ctx := context.Background()
	store := credprovider.NewMemoryStore()
	var bound, header []string
	var hasBound, hasHeader bool
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCtx := httpRequestContext(r.Context(), r)
		b, okB, err := toolset.BoundFromContext(reqCtx)
		require.NoError(t, err)
		h, okH, err := toolset.FromContext(reqCtx)
		require.NoError(t, err)
		bound, hasBound, header, hasHeader = b.Raw, okB, h.Raw, okH
		w.WriteHeader(http.StatusOK)
	})
	h := actorBearerHandler(testActorBearer(t, store), mcp, http.NotFoundHandler(), nil, nil)
	call := func(headerValue string) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+fakeActorJWT(testActorIssuer, "actor/kagent/session-a"))
		if headerValue != "" {
			req.Header.Set(toolset.HeaderName, headerValue)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	actor := "spiffe://substrate-actor.local/actor/kagent/session-a"

	require.NoError(t, store.Put(ctx, actor, credprovider.Binding{Subject: "alice", Session: "kagent/s1",
		Providers: []string{credprovider.MusterProvider}, Toolset: []string{"preset:read-only"}}, time.Hour))
	call("")
	assert.True(t, hasBound)
	assert.Equal(t, []string{"preset:read-only"}, bound)
	assert.False(t, hasHeader, "a request without the header is bound all the same")

	call("preset:full")
	assert.Equal(t, []string{"preset:read-only"}, bound, "the header never replaces the binding")
	assert.True(t, hasHeader)
	assert.Equal(t, []string{"preset:full"}, header)

	require.NoError(t, store.Put(ctx, actor, credprovider.Binding{Subject: "alice", Session: "kagent/s1",
		Providers: []string{credprovider.MusterProvider}}, time.Hour))
	call("")
	assert.False(t, hasBound, "a binding without a toolset leaves the request unscoped")
}

func TestActorBearerHandlerStoreOutage(t *testing.T) {
	h := actorBearerHandler(testActorBearer(t, outageStore{credprovider.NewMemoryStore()}), http.NotFoundHandler(), http.NotFoundHandler(), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+fakeActorJWT(testActorIssuer, "actor/kagent/session-a"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

type outageStore struct{ credprovider.Store }

func (outageStore) Get(context.Context, string) (*credprovider.Binding, error) {
	return nil, errors.New("valkey down")
}

func TestNewActorBearerNeedsTheCredentialProvider(t *testing.T) {
	a := &AggregatorServer{}
	ab, err := a.newActorBearer()
	require.NoError(t, err)
	assert.Nil(t, ab, "disabled by default")

	a.config.CredentialProvider.ActorBearer.Enabled = true
	_, err = a.newActorBearer()
	assert.ErrorContains(t, err, "credential provider")

	a.config.CredentialProvider = credentialProviderConfig()
	a.config.CredentialProvider.ActorBearer.Enabled = true
	a.config.CredentialProvider.ActorBearer.Issuer = testActorIssuer
	a.config.CredentialProvider.ActorBearer.Audience = "https://muster.example.com"
	a.credProvider, err = a.newCredentialProvider()
	require.NoError(t, err)
	ab, err = a.newActorBearer()
	require.NoError(t, err)
	assert.NotNil(t, ab)

	mux := http.NewServeMux()
	require.NoError(t, a.registerActorBearerRoutes(mux, http.NotFoundHandler(), http.NotFoundHandler(), nil))
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/mcp", nil))
	assert.Equal(t, "/mcp", pattern)
}
