package oauth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
	"github.com/giantswarm/muster/v5/internal/chatgptlogin/chatgptlogintest"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

const chatgptClientID = "app_codex"

// newChatGPTReplica is one muster replica with the ChatGPT App pinned the way
// the aggregator pins it: subject-scoped, a public client, OpenAI's token
// endpoint.
func newChatGPTReplica(t *testing.T, srv *miniredis.Miniredis, openai *chatgptlogintest.Server) *Client {
	t.Helper()
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid",
		WithTokenStorer(newValkeyTokenStoreOn(t, srv)))
	client.oauthClient = pkgoauth.NewClient(pkgoauth.WithHTTPClient(openai.Client()))
	client.PinIssuer(openai.URL, IssuerPin{ClientID: chatgptClientID, SubjectScoped: true}, &pkgoauth.Metadata{
		AuthorizationEndpoint: openai.URL + "/oauth/authorize",
		TokenEndpoint:         openai.URL + "/oauth/token",
	})
	return client
}

// signInToChatGPT runs the device-code sign-in against the fake, the person
// entering the code at once.
func signInToChatGPT(t *testing.T, openai *chatgptlogintest.Server) *pkgoauth.Token {
	t.Helper()
	openai.Enter()
	login := &chatgptlogin.Client{Issuer: openai.URL, ClientID: chatgptClientID, HTTP: openai.Client()}
	code, err := login.RequestCode(context.Background())
	if err != nil {
		t.Fatalf("request code: %v", err)
	}
	token, err := login.Await(context.Background(), code)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	return token
}

func TestChatGPTGrant_ReplicasRefreshOnce(t *testing.T) {
	openai := chatgptlogintest.New(t, chatgptClientID)
	valkeySrv := miniredis.RunT(t)

	const replicas = 3
	clients := make([]*Client, replicas)
	for i := range clients {
		clients[i] = newChatGPTReplica(t, valkeySrv, openai)
	}
	token := signInToChatGPT(t, openai)
	if token.ExpiresAt.IsZero() {
		t.Fatal("the grant's lifetime is read from the access token's exp")
	}
	signedIn := token.RefreshToken
	// Filed as the sign-in files it, then made due: the access token
	// expires within the refresh margin.
	token.ExpiresAt = time.Now().Add(time.Minute)
	token.ExpiresIn = 0
	clients[0].StoreToken("session-a", "alice", token)

	const callersPerReplica = 5
	results := make([]*pkgoauth.Token, replicas*callersPerReplica)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = clients[i%replicas].SubjectGrant(context.Background(), "alice", openai.URL)
		}(i)
	}
	wg.Wait()

	if n := openai.Redemptions(); n != 1 {
		t.Fatalf("refresh redemptions = %d, want exactly 1: OpenAI rotates the refresh token on use", n)
	}
	rotated := openai.RefreshToken()
	if rotated == signedIn {
		t.Fatal("the fake did not rotate the refresh token")
	}
	for i, got := range results {
		if got == nil || got.RefreshToken != rotated {
			t.Fatalf("caller %d (replica %d) got %+v, want the one refreshed grant", i, i%replicas, got)
		}
		if got.ExpiresAt.Before(time.Now().Add(30 * time.Minute)) {
			t.Errorf("caller %d: refreshed grant expires at %v, want the new token's exp", i, got.ExpiresAt)
		}
		if account := chatgptlogin.AccountID(got.IDToken); account != "acct-alice" {
			t.Errorf("caller %d: account id %q, want it kept with the grant", i, account)
		}
	}
	for i, client := range clients {
		if again := client.SubjectGrant(context.Background(), "alice", openai.URL); again == nil || again.RefreshToken != rotated {
			t.Errorf("replica %d re-read %+v, want the rotated grant without a refresh", i, again)
		}
	}
	if n := openai.Redemptions(); n != 1 {
		t.Errorf("redemptions after the re-reads = %d, want still 1", n)
	}
}
