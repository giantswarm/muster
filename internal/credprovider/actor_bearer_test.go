package credprovider

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

const testAudience = "https://muster.example.com"

// actorIssuer is a Substrate actor-JWT issuer: an ES256 key served as a key
// set over TLS at <issuer>/openid/v1/jwks.
type actorIssuer struct {
	url  string
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
	host string
}

func newActorIssuer(t *testing.T) *actorIssuer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: string(jose.ES256), Use: "sig"}}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ActorJWKSPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return &actorIssuer{url: srv.URL, key: key, pool: pool, host: u.Hostname()}
}

// mint signs an actor JWT; mutate edits its claims first.
func (i *actorIssuer) mint(t *testing.T, sub string, mutate func(*josejwt.Claims)) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: i.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	require.NoError(t, err)
	now := time.Now()
	claims := josejwt.Claims{
		Issuer:    i.url,
		Subject:   sub,
		Audience:  josejwt.Audience{testAudience},
		Expiry:    josejwt.NewNumericDate(now.Add(5 * time.Minute)),
		NotBefore: josejwt.NewNumericDate(now.Add(-time.Minute)),
		IssuedAt:  josejwt.NewNumericDate(now),
	}
	if mutate != nil {
		mutate(&claims)
	}
	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return token
}

func (i *actorIssuer) bearer(t *testing.T, store Store) *ActorBearer {
	t.Helper()
	verify, err := NewOIDCActorTokenVerifier(OIDCVerifierOptions{
		Issuer:              i.url,
		Audience:            testAudience,
		RootCAs:             i.pool,
		AllowPrivateIPHosts: []string{i.host},
	})
	require.NoError(t, err)
	ab, err := NewActorBearer(ActorBearerOptions{Issuer: i.url, Verify: verify, Store: store, BindingTTL: time.Hour})
	require.NoError(t, err)
	return ab
}

const testActorSubject = "actor/kagent/session-a" // testActor's sub

func TestActorBearerActsAsTheBoundPerson(t *testing.T) {
	ctx := context.Background()
	iss := newActorIssuer(t)
	store := NewMemoryStore()
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{"github", MusterProvider}}, time.Hour))
	ab := iss.bearer(t, store)

	token := iss.mint(t, testActorSubject, nil)
	require.True(t, ab.Claims(token))
	caller, err := ab.Authenticate(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "alice", caller.Subject)
	assert.Equal(t, "kagent/s1", caller.Session)
	assert.Equal(t, testActor, caller.Actor)
	assert.True(t, strings.HasPrefix(caller.SessionID, "actor-"))

	again, err := ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
	require.NoError(t, err)
	assert.Equal(t, caller.SessionID, again.SessionID, "one muster session across the actor's turns")

	require.NoError(t, store.Delete(ctx, testActor, "alice"))
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "bob", Session: "kagent/s9", Providers: []string{MusterProvider}}, time.Hour))
	rebound, err := ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
	require.NoError(t, err)
	assert.Equal(t, "bob", rebound.Subject)
	assert.NotEqual(t, caller.SessionID, rebound.SessionID, "a rebound actor never inherits another person's session")
}

func TestActorBearerCarriesTheBoundToolset(t *testing.T) {
	ctx := context.Background()
	iss := newActorIssuer(t)
	store := NewMemoryStore()
	ab := iss.bearer(t, store)

	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{MusterProvider}, Toolset: []string{"preset:read-only"}}, time.Hour))
	caller, err := ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
	require.NoError(t, err)
	assert.Equal(t, []string{"preset:read-only"}, caller.Toolset)

	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{MusterProvider}}, time.Hour))
	caller, err = ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
	require.NoError(t, err)
	assert.Nil(t, caller.Toolset, "a binding without a toolset bounds nothing")
}

func TestActorBearerCarriesTheBindersSignInIdentity(t *testing.T) {
	ctx := context.Background()
	iss := newActorIssuer(t)
	store := NewMemoryStore()
	ab := iss.bearer(t, store)

	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{MusterProvider},
		SignInIssuer: "https://dex.example.com", SignInSubject: "raw-alice"}, time.Hour))
	caller, err := ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
	require.NoError(t, err)
	assert.Equal(t, api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "raw-alice"}, caller.SignIn)

	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{MusterProvider}}, time.Hour))
	caller, err = ab.Authenticate(ctx, iss.mint(t, testActorSubject, nil))
	require.NoError(t, err)
	assert.Zero(t, caller.SignIn, "a binding that recorded none carries none")
}

func TestActorBearerRefusals(t *testing.T) {
	ctx := context.Background()
	iss := newActorIssuer(t)
	other := newActorIssuer(t)
	store := NewMemoryStore()
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{MusterProvider}}, time.Hour))
	gitOnly := "spiffe://substrate-actor.local/actor/kagent/session-git"
	require.NoError(t, store.Put(ctx, gitOnly, Binding{Subject: "alice", Session: "kagent/s2", Providers: []string{"github"}}, time.Hour))
	ab := iss.bearer(t, store)

	cases := map[string]struct {
		token string
		want  error
	}{
		"unbound actor":            {iss.mint(t, "actor/kagent/session-z", nil), ErrActorNotAllowed},
		"binding without muster":   {iss.mint(t, "actor/kagent/session-git", nil), ErrActorNotAllowed},
		"another host's audience":  {iss.mint(t, testActorSubject, func(c *josejwt.Claims) { c.Audience = josejwt.Audience{"https://api.github.com"} }), ErrInvalidActorToken},
		"expired":                  {iss.mint(t, testActorSubject, func(c *josejwt.Claims) { c.Expiry = josejwt.NewNumericDate(time.Now().Add(-time.Hour)) }), ErrInvalidActorToken},
		"signed by another key":    {other.mint(t, testActorSubject, func(c *josejwt.Claims) { c.Issuer = iss.url }), ErrInvalidActorToken},
		"not an actor subject":     {iss.mint(t, "system:serviceaccount:kagent:x", nil), ErrInvalidActorToken},
		"actor subject with extra": {iss.mint(t, "actor/kagent/a/b", nil), ErrInvalidActorToken},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ab.Authenticate(ctx, tc.token)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestActorBearerAuditsEveryAuthentication(t *testing.T) {
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	ctx := t.Context()
	iss := newActorIssuer(t)
	store := NewMemoryStore()
	const person = "CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs"
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: person, Session: "kagent/s1", Providers: []string{MusterProvider}}, time.Hour))
	gitOnly := "spiffe://substrate-actor.local/actor/kagent/session-git"
	require.NoError(t, store.Put(ctx, gitOnly, Binding{Subject: person, Session: "kagent/s2", Providers: []string{"github"}}, time.Hour))
	scoped := "spiffe://substrate-actor.local/actor/kagent/session-ro"
	require.NoError(t, store.Put(ctx, scoped, Binding{Subject: person, Session: "kagent/s3", Providers: []string{MusterProvider}, Toolset: []string{"preset:read-only", "tool:x_gh_issues"}}, time.Hour))
	ab := iss.bearer(t, store)

	cases := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "accepted",
			token: iss.mint(t, testActorSubject, nil),
			want:  "[AUDIT] action=actor_bearer outcome=success subject=CiQwOGE4... target=muster details=actor=" + testActor + " session=kagent/s1",
		},
		{
			name:  "accepted with a bound toolset",
			token: iss.mint(t, "actor/kagent/session-ro", nil),
			want:  "[AUDIT] action=actor_bearer outcome=success subject=CiQwOGE4... target=muster details=actor=" + scoped + " session=kagent/s3 toolset=[preset:read-only,tool:x_gh_issues]",
		},
		{
			name:  "binding without muster",
			token: iss.mint(t, "actor/kagent/session-git", nil),
			want:  "[AUDIT] action=actor_bearer outcome=failure subject=CiQwOGE4... target=muster details=actor=" + gitOnly + " session=kagent/s2 error=the binding does not list muster",
		},
		{
			name:  "unbound actor",
			token: iss.mint(t, "actor/kagent/session-z", nil),
			want:  "[AUDIT] action=actor_bearer outcome=failure target=muster details=actor=spiffe://substrate-actor.local/actor/kagent/session-z error=actor is not bound",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			_, _ = ab.Authenticate(ctx, tc.token)
			assert.Contains(t, buf.String(), tc.want)
			assert.NotContains(t, buf.String(), tc.token, "the actor JWT is never logged")
		})
	}
}

func TestActorBearerClaimsOnlyItsIssuer(t *testing.T) {
	iss := newActorIssuer(t)
	other := newActorIssuer(t)
	ab := iss.bearer(t, NewMemoryStore())
	assert.True(t, ab.Claims(iss.mint(t, testActorSubject, nil)))
	assert.False(t, ab.Claims(other.mint(t, testActorSubject, nil)), "another issuer's JWT goes to the OAuth chain")
	assert.False(t, ab.Claims("opaque-muster-token"))
	assert.False(t, ab.Claims("a.b.c"))
}

// failingStore fails every lookup.
type failingStore struct{ Store }

func (failingStore) Get(context.Context, string) (*Binding, error) {
	return nil, errors.New("valkey down")
}

func TestActorBearerStoreOutage(t *testing.T) {
	iss := newActorIssuer(t)
	ab := iss.bearer(t, failingStore{NewMemoryStore()})
	_, err := ab.Authenticate(context.Background(), iss.mint(t, testActorSubject, nil))
	assert.ErrorIs(t, err, ErrBindingsUnavailable)
}

func TestActorBearerValidatesOptions(t *testing.T) {
	verify := func(context.Context, string) (string, time.Time, error) { return "", time.Time{}, nil }
	_, err := NewActorBearer(ActorBearerOptions{Issuer: "http://idp.substrate.svc", Verify: verify, Store: NewMemoryStore()})
	assert.Error(t, err, "issuer must be https")
	_, err = NewActorBearer(ActorBearerOptions{Issuer: "https://idp.substrate.svc", Store: NewMemoryStore()})
	assert.Error(t, err, "verifier required")
	_, err = NewOIDCActorTokenVerifier(OIDCVerifierOptions{Issuer: "https://idp.substrate.svc"})
	assert.Error(t, err, "audience required")
}
