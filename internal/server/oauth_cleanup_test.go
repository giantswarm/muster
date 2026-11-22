package server

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	miniredisserver "github.com/alicebob/miniredis/v2/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestOAuthValkeyConnectionsCloseOnShutdown(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "graceful", true: "cancelled"}[cancelled], func(t *testing.T) {
			backend := miniredis.RunT(t)
			server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
				Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
				Google:  config.GoogleConfig{ClientID: "muster", ClientSecret: "fixture-secret"},
				Storage: config.OAuthStorageConfig{Type: "valkey", Valkey: config.ValkeyConfig{URL: backend.Addr(), KeyPrefix: "shutdown:"}},
			}, http.NotFoundHandler(), false)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })
			require.Positive(t, backend.CurrentConnectionCount())
			shutdownContext := context.WithoutCancel(t.Context())
			if cancelled {
				var cancel context.CancelFunc
				shutdownContext, cancel = context.WithCancel(shutdownContext)
				cancel()
			}
			_ = server.Shutdown(shutdownContext)
			require.Eventually(t, func() bool { return backend.CurrentConnectionCount() == 0 }, time.Second, time.Millisecond)
			require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context())))
		})
	}
}

func TestOAuthConstructorFailureClosesValkeyConnections(t *testing.T) {
	for _, failure := range []string{"lifetimes", "options", "consent"} {
		t.Run(failure, func(t *testing.T) {
			backend := miniredis.RunT(t)
			configuration := config.OAuthServerConfig{
				Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
				Google:  config.GoogleConfig{ClientID: "muster", ClientSecret: "fixture-secret"},
				Storage: config.OAuthStorageConfig{Type: "valkey", Valkey: config.ValkeyConfig{URL: backend.Addr(), KeyPrefix: "failure:"}},
			}
			var failedConsentWrites atomic.Int32
			switch failure {
			case "lifetimes":
				configuration.SessionDuration = "invalid"
			case "options":
				configuration.TrustedProxyCIDRs = []string{"invalid"}
			case "consent":
				backend.Server().SetPreHook(func(peer *miniredisserver.Peer, command string, arguments ...string) bool {
					if strings.EqualFold(command, "SET") && len(arguments) > 0 && strings.Contains(arguments[0], "consent_cookie_key:") {
						failedConsentWrites.Add(1)
						peer.WriteError("ERR consent key storage unavailable")
						return true
					}
					return false
				})
			}
			for range 3 {
				server, err := NewOAuthHTTPServer(configuration, http.NotFoundHandler(), false)
				require.Error(t, err)
				require.Nil(t, server)
				require.Eventually(t, func() bool { return backend.CurrentConnectionCount() == 0 }, time.Second, time.Millisecond)
			}
			if failure == "consent" {
				require.Positive(t, failedConsentWrites.Load())
			}
		})
	}
}
