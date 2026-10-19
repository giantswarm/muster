package oauth

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/valkey-io/valkey-go"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// newValkeyTokenStoreOn is one replica's token store on a key space the
// replicas share, with its own Valkey connection.
func newValkeyTokenStoreOn(t *testing.T, srv *miniredis.Miniredis) *ValkeyTokenStore {
	t.Helper()
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{srv.Addr()},
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("valkey client: %v", err)
	}
	t.Cleanup(client.Close)
	return NewValkeyTokenStore(client, DefaultTokenStoreTTL, "muster:", nil)
}

// newReplica is one muster replica: its own Client (and so its own
// in-process single-flight) on the shared Valkey key space, with the
// issuer pinned like newGrantClient does.
func newReplica(t *testing.T, srv *miniredis.Miniredis, as *httptest.Server) *Client {
	t.Helper()
	issuer := as.URL
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid",
		WithTokenStorer(newValkeyTokenStoreOn(t, srv)))
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(as.Client()))
	client.PinIssuer(issuer, IssuerPin{ClientID: "Iv23liPinned", ClientSecret: "s3cr3t", SubjectScoped: true}, &pkgoauth.Metadata{
		AuthorizationEndpoint: issuer + "/authorize",
		TokenEndpoint:         issuer + "/token",
	})
	return client
}

func TestSubjectGrant_ReplicasRefreshOnce(t *testing.T) {
	as := newRotatingAS("ghr_initial")
	as.delay = 100 * time.Millisecond
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	valkeySrv := miniredis.RunT(t)

	const replicas = 4
	clients := make([]*Client, replicas)
	for i := range clients {
		clients[i] = newReplica(t, valkeySrv, srv)
	}
	issuer := srv.URL
	storeDueGrant(clients[0], issuer, "ghr_initial", "session-a", "session-b")

	const callersPerReplica = 6
	results := make([]*pkgoauth.Token, replicas*callersPerReplica)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := []string{"session-a", "session-b"}[i%2]
			results[i] = clients[i%replicas].GetByIssuerForUser(session, "alice", issuer)
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got == nil || got.AccessToken != "ghu_refreshed_y" {
			t.Errorf("caller %d (replica %d) got %+v, want the single refreshed token", i, i%replicas, got)
		}
	}
	// One redemption across all replicas: a second one would have been
	// rejected by the rotating AS and cost the person the grant.
	if n := as.requests.Load(); n != 1 {
		t.Errorf("token endpoint requests = %d, want exactly 1", n)
	}
	for i, client := range clients {
		grant := client.SubjectGrant(context.Background(), "alice", issuer)
		if grant == nil || grant.RefreshToken != "ghr_rotated_x" {
			t.Errorf("replica %d sees grant %+v, want the rotated refresh token", i, grant)
		}
	}
	if n := as.requests.Load(); n != 1 {
		t.Errorf("token endpoint requests after the re-reads = %d, want still 1", n)
	}
}

func TestSubjectGrant_RotatedElsewhereKeepsTheGrant(t *testing.T) {
	as := newRotatingAS("ghr_other")
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)
	storeDueGrant(client, issuer, "ghr_initial", "session-a")

	// While this replica redeems ghr_initial, another writer files a
	// rotated grant; the AS then rejects ghr_initial as already used.
	as.beforeAnswer = func() {
		client.StoreToken("session-a", "alice", &pkgoauth.Token{
			AccessToken:  "ghu_other_replica",
			TokenType:    "Bearer",
			RefreshToken: "ghr_other",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(time.Hour),
			Scope:        "repo",
			Issuer:       issuer,
		})
	}

	got := client.GetByIssuerForUser("session-a", "alice", issuer)
	if got == nil || got.AccessToken != "ghu_other_replica" {
		t.Fatalf("got %+v, want the grant the other writer stored", got)
	}
	if grant := client.tokenStore.GetByIssuerIncludingExpired(subjectSessionID("alice"), issuer); grant == nil || grant.RefreshToken != "ghr_other" {
		t.Errorf("grant after the rejected redemption = %+v, want the other writer's", grant)
	}
}

func TestSubjectGrant_RotatedElsewhereIsNeverDeleted(t *testing.T) {
	as := newRotatingAS("ghr_other")
	srv := httptest.NewTLSServer(as.handler(t))
	defer srv.Close()
	client, issuer := newGrantClient(t, srv)
	storeDueGrant(client, issuer, "ghr_initial", "session-a")

	// The other writer's copy carries an access token that is no longer
	// accepted: the lookup has nothing to hand out, but the grant and its
	// refresh token stay for the next lookup to redeem.
	as.beforeAnswer = func() {
		client.StoreToken("session-a", "alice", &pkgoauth.Token{
			AccessToken:  "ghu_other_replica",
			TokenType:    "Bearer",
			RefreshToken: "ghr_other",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(-time.Minute),
			Scope:        "repo",
			Issuer:       issuer,
		})
	}

	if got := client.GetByIssuerForUser("session-a", "alice", issuer); got != nil && got.RefreshToken == "ghr_initial" {
		t.Fatalf("got the redeemed grant %+v", got)
	}
	grant := client.tokenStore.GetByIssuerIncludingExpired(subjectSessionID("alice"), issuer)
	if grant == nil || grant.RefreshToken != "ghr_other" {
		t.Fatalf("grant after the rejected redemption = %+v, want the other writer's kept", grant)
	}
}

func TestValkeyTokenStore_LockGrantRefresh(t *testing.T) {
	valkeySrv := miniredis.RunT(t)
	replicaA := newValkeyTokenStoreOn(t, valkeySrv)
	replicaB := newValkeyTokenStoreOn(t, valkeySrv)
	ctx := context.Background()

	unlockA, err := replicaA.LockGrantRefresh(ctx, "alice", "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	// Another person, or another issuer, is not held up.
	unlockOther, err := replicaB.LockGrantRefresh(ctx, "bob", "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("lock for another person: %v", err)
	}
	unlockOther()

	// The same grant waits for the holder and gives up with its context.
	waitCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := replicaB.LockGrantRefresh(waitCtx, "alice", "https://gitlab.example.com"); err == nil {
		t.Fatal("second lock on a held grant succeeded")
	}

	acquired := make(chan func())
	go func() {
		unlock, err := replicaB.LockGrantRefresh(ctx, "alice", "https://gitlab.example.com")
		if err != nil {
			t.Errorf("lock after release: %v", err)
		}
		acquired <- unlock
	}()
	unlockA()
	unlockB := <-acquired

	// A holder whose lock expired never releases the next holder's.
	valkeySrv.FastForward(grantRefreshLockTTL + time.Second)
	unlockC, err := replicaA.LockGrantRefresh(ctx, "alice", "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("lock after expiry: %v", err)
	}
	unlockB()
	key := replicaA.grantRefreshLockKey("alice", "https://gitlab.example.com")
	if !valkeySrv.Exists(key) {
		t.Error("an expired holder's unlock released the current holder's lock")
	}
	unlockC()
	if valkeySrv.Exists(key) {
		t.Error("lock still held after its holder released it")
	}
}

func TestValkeyTokenStore_LockGrantRefreshUnavailable(t *testing.T) {
	valkeySrv := miniredis.RunT(t)
	store := newValkeyTokenStoreOn(t, valkeySrv)
	valkeySrv.SetError("LOADING")

	if _, err := store.LockGrantRefresh(context.Background(), "alice", "https://gitlab.example.com"); err == nil {
		t.Fatal("lock succeeded against a failing Valkey")
	}
}
