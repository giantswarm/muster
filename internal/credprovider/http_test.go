package credprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// asPerson stands in for muster's ValidateTokenWithSubject: the person the
// bearer was validated to.
func asPerson(sub string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sub != "" {
			r = r.WithContext(api.WithSubject(r.Context(), sub))
		}
		h(w, r)
	})
}

func bind(t *testing.T, p *Provider, sub, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	asPerson(sub, p.HandleBind).ServeHTTP(rec, httptest.NewRequest(http.MethodPut, BindingsPath, strings.NewReader(body)))
	return rec
}

func unbind(t *testing.T, p *Provider, sub, actor string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	asPerson(sub, p.HandleUnbind).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, BindingsPath+"?actor="+url.QueryEscape(actor), nil))
	return rec
}

const testBindBody = `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"]}`

func TestBindAndUnbindAsThePerson(t *testing.T) {
	p, store := newTestProvider(t, fakeGrants{})
	ctx := context.Background()

	rec := bind(t, p, "alice", testBindBody)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	b, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "alice", b.Subject, "the binding is the caller's")
	assert.Equal(t, "kagent/s1", b.Session)
	assert.Equal(t, []string{"github"}, b.Providers)

	assert.Equal(t, http.StatusNoContent, bind(t, p, "alice", testBindBody).Code, "a repeated bind succeeds")
	assert.Equal(t, http.StatusForbidden, bind(t, p, "mallory", testBindBody).Code, "a bind by anyone but the person is refused")
	assert.Equal(t, http.StatusForbidden, unbind(t, p, "mallory", testActor).Code, "so is an unbind")

	assert.Equal(t, http.StatusNoContent, unbind(t, p, "alice", testActor).Code)
	b, err = store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.Nil(t, b)
	assert.Equal(t, http.StatusNoContent, unbind(t, p, "alice", testActor).Code, "unbind is idempotent")
}

func TestBindExpiresWithTheSessionIdleTTL(t *testing.T) {
	p, store := newTestProvider(t, fakeGrants{})
	now := time.Now()
	store.now = func() time.Time { return now }
	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", testBindBody).Code)
	now = now.Add(time.Hour)
	b, err := store.Get(context.Background(), testActor)
	require.NoError(t, err)
	assert.Nil(t, b, "the provider's binding TTL (one hour here) has passed")
}

func TestBindRejectsBadRequests(t *testing.T) {
	p, _ := newTestProvider(t, fakeGrants{})
	cases := map[string]struct {
		sub, body string
		code      int
	}{
		"no identity":                     {"", testBindBody, http.StatusUnauthorized},
		"not JSON":                        {"alice", "{", http.StatusBadRequest},
		"unknown field":                   {"alice", `{"actor":"` + testActor + `","session":"s","providers":["github"],"subject":"bob"}`, http.StatusBadRequest},
		"not an actor":                    {"alice", `{"actor":"spiffe://cluster.local/ns/x/sa/y","session":"s","providers":["github"]}`, http.StatusBadRequest},
		"no session":                      {"alice", `{"actor":"` + testActor + `","providers":["github"]}`, http.StatusBadRequest},
		"no providers":                    {"alice", `{"actor":"` + testActor + `","session":"s"}`, http.StatusBadRequest},
		"not releasable":                  {"alice", `{"actor":"` + testActor + `","session":"s","providers":["login"]}`, http.StatusBadRequest},
		"muster without the actor bearer": {"alice", `{"actor":"` + testActor + `","session":"s","providers":["muster"]}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.code, bind(t, p, tc.sub, tc.body).Code)
		})
	}
	t.Run("unbind without identity or actor", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, unbind(t, p, "", testActor).Code)
		assert.Equal(t, http.StatusBadRequest, unbind(t, p, "alice", "").Code)
	})
}

func TestBindListsMusterWithTheActorBearer(t *testing.T) {
	store := NewMemoryStore()
	p, err := NewProvider(Options{
		GatewayIdentity: testGateway,
		Releasable:      map[string]string{"github": testIssuer},
		MusterBearer:    true,
		Store:           store,
		Grants:          fakeGrants{}.source,
	})
	require.NoError(t, err)
	body := `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github","muster"]}`
	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", body).Code)
	b, err := store.Get(context.Background(), testActor)
	require.NoError(t, err)
	assert.True(t, b.Allows(MusterProvider))
}
