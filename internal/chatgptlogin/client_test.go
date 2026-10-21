package chatgptlogin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
	"github.com/giantswarm/muster/v5/internal/chatgptlogin/chatgptlogintest"
)

const clientID = "app_test"

// pollClock is a client whose wait between polls returns at once and whose
// clock advances by the waited interval, so a test walks a code's lifetime
// without sleeping.
func pollClock(server *chatgptlogintest.Server, onWait func()) *chatgptlogin.Client {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	return &chatgptlogin.Client{
		Issuer:   server.URL,
		ClientID: clientID,
		HTTP:     server.Client(),
		Now:      func() time.Time { return now },
		Wait: func(ctx context.Context, d time.Duration) error {
			now = now.Add(d)
			if onWait != nil {
				onWait()
			}
			return ctx.Err()
		},
	}
}

func TestDeviceCode_PendingThenConnected(t *testing.T) {
	server := chatgptlogintest.New(t, clientID)
	waits := 0
	client := pollClock(server, func() {
		waits++
		if waits == 2 {
			server.Enter()
		}
	})

	code, err := client.RequestCode(context.Background())
	require.NoError(t, err)
	assert.Equal(t, chatgptlogintest.UserCode, code.UserCode)
	assert.Equal(t, server.URL+"/codex/device", code.VerificationURL)
	assert.Equal(t, client.Now().Add(chatgptlogin.CodeLifetime), code.ExpiresAt)

	token, err := client.Await(context.Background(), code)
	require.NoError(t, err)
	assert.Equal(t, 3, server.Polls(), "pending twice, then entered")
	assert.Equal(t, 1, server.Exchanges())
	assert.Equal(t, server.URL, token.Issuer)
	assert.Equal(t, server.RefreshToken(), token.RefreshToken)
	assert.False(t, token.ExpiresAt.IsZero(), "the lifetime comes from the access token's exp")
	assert.Equal(t, "acct-alice", chatgptlogin.AccountID(token.IDToken))
}

func TestDeviceCode_PollsAtTheReturnedInterval(t *testing.T) {
	server := chatgptlogintest.New(t, clientID)
	var waited []time.Duration
	client := pollClock(server, nil)
	wait := client.Wait
	client.Wait = func(ctx context.Context, d time.Duration) error {
		waited = append(waited, d)
		server.Enter()
		return wait(ctx, d)
	}
	code, err := client.RequestCode(context.Background())
	require.NoError(t, err)
	_, err = client.Await(context.Background(), code)
	require.NoError(t, err)
	assert.Equal(t, []time.Duration{5 * time.Second}, waited)
}

func TestDeviceCode_ExpiresAfterFifteenMinutes(t *testing.T) {
	server := chatgptlogintest.New(t, clientID)
	client := pollClock(server, nil)

	code, err := client.RequestCode(context.Background())
	require.NoError(t, err)
	_, err = client.Await(context.Background(), code)
	require.ErrorIs(t, err, chatgptlogin.ErrExpired)
	assert.Equal(t, int(chatgptlogin.CodeLifetime/(5*time.Second))+1, server.Polls(), "polled every interval for 15 minutes")
	assert.Zero(t, server.Exchanges())
}

func TestDeviceCode_NotEnabledIs404(t *testing.T) {
	server := chatgptlogintest.New(t, clientID)
	server.Disable()

	_, err := pollClock(server, nil).RequestCode(context.Background())
	require.ErrorIs(t, err, chatgptlogin.ErrNotEnabled)
	assert.Contains(t, err.Error(), "Settings → Security")
}

func TestDeviceCode_StopsWithItsContext(t *testing.T) {
	server := chatgptlogintest.New(t, clientID)
	ctx, cancel := context.WithCancel(context.Background())
	client := pollClock(server, cancel)

	code, err := client.RequestCode(ctx)
	require.NoError(t, err)
	_, err = client.Await(ctx, code)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDeviceCode_RefusedPollFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			_ = json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "d", "usercode": "U-1", "interval": 7})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	client := &chatgptlogin.Client{Issuer: srv.URL, ClientID: clientID}

	code, err := client.RequestCode(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "U-1", code.UserCode, "the usercode spelling and a numeric interval are read too")
	_, err = client.Await(context.Background(), code)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestAccountID(t *testing.T) {
	assert.Equal(t, "acct-1", chatgptlogin.AccountID(chatgptlogintest.IDToken("acct-1")))
	assert.Empty(t, chatgptlogin.AccountID(""))
	assert.Empty(t, chatgptlogin.AccountID("not-a-jwt"))
	assert.Empty(t, chatgptlogin.AccountID(chatgptlogintest.AccessToken(1, time.Now())), "no auth claim")
}
