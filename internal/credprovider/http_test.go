package credprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
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

// The binding records who the bind request's bearer was issued to, the
// identity a sign-in link of the actor's requests must be completed by.
func TestBindRecordsTheSignInIdentity(t *testing.T) {
	p, store := newTestProvider(t, fakeGrants{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, BindingsPath, strings.NewReader(testBindBody))
	req = req.WithContext(api.WithSignInIdentity(req.Context(), api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "raw-alice"}))
	asPerson("alice@example.com", p.HandleBind).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	b, err := store.Get(context.Background(), testActor)
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "alice@example.com", b.Subject)
	assert.Equal(t, "https://dex.example.com", b.SignInIssuer)
	assert.Equal(t, "raw-alice", b.SignInSubject, "the raw sub, not the session subject")
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
	selectors := make([]string, toolset.MaxInlineSelectors+1)
	for i := range selectors {
		selectors[i] = fmt.Sprintf(`"tool:t%d"`, i)
	}
	cases := map[string]struct{ toolset, want string }{
		"empty":     {`[]`, "is empty"},
		"reserved":  {`["toolset:shared"]`, "reserved for shared toolsets"},
		"label":     {`["label:tier=infra"]`, "allowed inside presets only"},
		"malformed": {`["preset:none","garbage"]`, "malformed"},
		"too many":  {"[" + strings.Join(selectors, ",") + "]", "more than the 128 allowed inline"},
		"too large": {`["tool:` + strings.Repeat("x", 8188) + `"]`, "encodes to 8193 bytes"},
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

func TestBindAcceptsLargerToolsets(t *testing.T) {
	for _, count := range []int{33, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			provider, store := newTestProvider(t, fakeGrants{})
			selectors := make([]string, count)
			for index := range selectors {
				selectors[index] = fmt.Sprintf("tool:x_example_%d", index)
			}
			body, err := json.Marshal(BindRequest{Actor: testActor, Session: "kagent/s1", Providers: []string{"github"}, Toolset: selectors})
			require.NoError(t, err)
			response := bind(t, provider, "alice", string(body))
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			stored, err := store.Get(t.Context(), testActor)
			require.NoError(t, err)
			require.Equal(t, selectors, stored.Toolset)
		})
	}
}
