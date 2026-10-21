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

func TestBindStoresTheToolset(t *testing.T) {
	p, store := newTestProvider(t, fakeGrants{})
	ctx := context.Background()

	body := `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"],"toolset":["preset:read-only","tool:x_gh_issues"]}`
	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", body).Code)
	b, err := store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.Equal(t, []string{"preset:read-only", "tool:x_gh_issues"}, b.Toolset)

	body = `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"],"toolset":["preset:not-configured"]}`
	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", body).Code, "an unknown preset fails the Session's meta-tool calls, not the bind")

	require.Equal(t, http.StatusNoContent, bind(t, p, "alice", testBindBody).Code)
	b, err = store.Get(ctx, testActor)
	require.NoError(t, err)
	assert.Nil(t, b.Toolset, "a bind without a toolset binds none")
}

func TestBindRejectsBadToolsets(t *testing.T) {
	p, _ := newTestProvider(t, fakeGrants{})
	withToolset := func(toolset string) string {
		return `{"actor":"` + testActor + `","session":"kagent/s1","providers":["github"],"toolset":` + toolset + `}`
	}
	selectors := make([]string, 33)
	for i := range selectors {
		selectors[i] = `"tool:t` + strings.Repeat("x", i+1) + `"`
	}
	cases := map[string]struct{ toolset, want string }{
		"empty":     {`[]`, "is empty"},
		"reserved":  {`["toolset:shared"]`, "reserved for shared toolsets"},
		"label":     {`["label:tier=infra"]`, "allowed inside presets only"},
		"malformed": {`["preset:none","garbage"]`, "malformed"},
		"too many":  {"[" + strings.Join(selectors, ",") + "]", "more than the 32 allowed inline"},
		"not list":  {`"preset:none"`, "bad request"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := bind(t, p, "alice", withToolset(tc.toolset))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}
}
