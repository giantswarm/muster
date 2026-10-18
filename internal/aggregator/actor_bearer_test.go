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
)

const testActorIssuer = "https://idp.substrate.svc"

// fakeActorJWT carries iss and sub; the test verifier trusts it.
func fakeActorJWT(iss, sub string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"ES256"}`)) + "." + enc([]byte(`{"iss":"`+iss+`","sub":"`+sub+`"}`)) + ".sig"
}

func testActorBearer(t *testing.T, store credprovider.Store) *credprovider.ActorBearer {
	t.Helper()
	verify := func(_ context.Context, token string) (string, time.Time, error) {
		if token == fakeActorJWT(testActorIssuer, "actor/kagent/forged") {
			return "", time.Time{}, errors.New("signature invalid")
		}
		return "actor/kagent/session-a", time.Now().Add(time.Minute), nil
	}
	ab, err := credprovider.NewActorBearer(credprovider.ActorBearerOptions{Issuer: testActorIssuer, Verify: verify, Store: store})
	require.NoError(t, err)
	return ab
}

func TestActorBearerHandler(t *testing.T) {
	ctx := context.Background()
	store := credprovider.NewMemoryStore()
	ab := testActorBearer(t, store)

	var served struct{ sub, session, bearer string }
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.sub = api.GetSubjectFromContext(r.Context())
		served.session = api.GetSessionIDFromContext(r.Context())
		served.bearer = server.CallerTokensFromContext(r.Context()).Bearer
		w.WriteHeader(http.StatusOK)
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	var authenticated string
	h := actorBearerHandler(ab, mcp, next, func(_ context.Context, sessionID string) { authenticated = sessionID })

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
}

func TestActorBearerHandlerStoreOutage(t *testing.T) {
	h := actorBearerHandler(testActorBearer(t, outageStore{credprovider.NewMemoryStore()}), http.NotFoundHandler(), http.NotFoundHandler(), nil)
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
