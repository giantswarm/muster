package aggregator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/giantswarm/mcp-oauth/security"
	mcptoolkitlogging "github.com/giantswarm/mcp-toolkit/logging"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/config"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The session auth and capability stores are the aggregator's memory of which
// session is signed in to which server and what each session may see. On
// Valkey (oauth.server.storage.type: valkey) they outlive a restart and are
// visible to the admin UI and to an operator; in memory they die with the pod.
// A deployment that configured Valkey must therefore never run on the
// in-memory stores (giantswarm/muster#1229). A Valkey that does not answer
// while muster starts -- both starting together after a node roll, Valkey
// being rescheduled -- is waited for with capped backoff for as long as it
// takes, not given up on after a few attempts: exiting only turns a Valkey
// outage into a crash loop and a restart page (giantswarm/muster#1364). The
// port is not bound during the wait, so the pod is not ready; the chart's
// startup probe keeps the liveness probe off until then and bounds the wait.
// Only a configuration error (credentials or database refused, a malformed
// address, a certificate that does not verify) ends the start. The in-memory
// stores are for a deployment that configured no Valkey.

const (
	sessionStoreInitialBackoff = time.Second
	sessionStoreMaxBackoff     = 8 * time.Second
)

// storeBundle groups the results of createStores.
type storeBundle struct {
	authStore       oauthstore.SessionAuthStore
	capabilityStore oauthstore.CapabilityStore
	valkeyClient    valkey.Client
	keyPrefix       string
	encryptor       *security.Encryptor
}

// createStores builds the session auth and capability stores from the OAuth
// server storage configuration: Valkey-backed stores on one shared client when
// the storage type is "valkey", in-memory stores otherwise. A configured
// Valkey that is not answering yet is waited for (connectValkey); one that
// stays away is an error, never a silent fallback to memory.
func createStores(ctx context.Context, cfg AggregatorConfig) (storeBundle, error) {
	oauthCfg, ok := cfg.OAuthServer.Config.(config.OAuthServerConfig)
	if !ok || oauthCfg.Storage.Type != "valkey" || oauthCfg.Storage.Valkey.URL == "" {
		logging.Info("Aggregator", "Using in-memory session auth and capability stores")
		recordSessionStoreBackend(ctx, sessionStoreBackendMemory)
		return storeBundle{
			authStore:       oauthstore.NewInMemorySessionAuthStore(oauthstore.DefaultCapabilityStoreTTL),
			capabilityStore: oauthstore.NewInMemoryCapabilityStore(oauthstore.DefaultCapabilityStoreTTL),
			keyPrefix:       config.DefaultValkeyKeyPrefix,
		}, nil
	}

	keyPrefix := oauthCfg.Storage.Valkey.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = config.DefaultValkeyKeyPrefix
	}

	client, err := connectValkey(ctx, oauthCfg.Storage.Valkey, sleepCtx)
	if err != nil {
		logging.Error("Aggregator", err, "Configured Valkey did not answer; refusing to serve sessions on in-memory stores")
		return storeBundle{}, err
	}

	logging.InfoWithAttrs("Aggregator", "Using Valkey-backed session auth and capability stores",
		slog.String("address", mcptoolkitlogging.RedactHost(oauthCfg.Storage.Valkey.URL)))
	recordSessionStoreBackend(ctx, sessionStoreBackendValkey)
	return storeBundle{
		authStore:       oauthstore.NewValkeySessionAuthStore(client, oauthstore.DefaultCapabilityStoreTTL, keyPrefix),
		capabilityStore: oauthstore.NewValkeyCapabilityStore(client, oauthstore.DefaultCapabilityStoreTTL, keyPrefix),
		valkeyClient:    client,
		keyPrefix:       keyPrefix,
		encryptor:       createEncryptor(oauthCfg),
	}, nil
}

// connectValkey dials the configured Valkey until it answers, backing off
// between attempts (1 s doubling to 8 s, the OAuth server's cadence for its
// own discovery) and logging one line per attempt. It returns early only on a
// configuration error (isValkeyConfigError) or when ctx ends. wait is the
// pause between attempts; tests pass one that starts the server instead of
// sleeping.
func connectValkey(ctx context.Context, cfg config.ValkeyConfig, wait func(context.Context, time.Duration) error) (valkey.Client, error) {
	address := mcptoolkitlogging.RedactHost(cfg.URL)
	backoff := sessionStoreInitialBackoff
	for attempt := 1; ; attempt++ {
		client, err := newValkeyClient(cfg)
		if err == nil {
			if attempt > 1 {
				logging.InfoWithAttrs("Aggregator", "Valkey for the session stores answered",
					slog.String("address", address), slog.Int("attempts", attempt))
			}
			return client, nil
		}
		if isValkeyConfigError(err) {
			return nil, fmt.Errorf("valkey %s refused the configuration: %w", address, err)
		}

		logging.WarnWithAttrs("Aggregator", "Valkey for the session stores did not answer, retrying",
			slog.String("address", address),
			slog.Int("attempt", attempt),
			slog.Duration("retry_in", backoff),
			slog.String("error", err.Error()))

		if waitErr := wait(ctx, backoff); waitErr != nil {
			return nil, fmt.Errorf("waiting for valkey %s (%d attempts, last error: %v): %w",
				address, attempt, err, waitErr)
		}
		backoff = min(backoff*2, sessionStoreMaxBackoff)
	}
}

// isValkeyConfigError tells a start that cannot succeed however long it waits
// from a Valkey that is not answering yet. Valkey answered and refused
// (WRONGPASS, NOAUTH, a database index out of range) -- except for the
// replies of a server that is still coming up (LOADING, TRYAGAIN,
// CLUSTERDOWN); the address does not parse; the server's certificate does not
// verify. Everything else -- refused or reset connections, timeouts, a
// service name that does not resolve yet -- is waited out.
func isValkeyConfigError(err error) bool {
	var verr *valkey.ValkeyError
	if errors.As(err, &verr) {
		return !verr.IsNil() && !verr.IsLoading() && !verr.IsTryAgain() && !verr.IsClusterDown()
	}
	var (
		addrErr      *net.AddrError
		unknownAuth  x509.UnknownAuthorityError
		hostname     x509.HostnameError
		invalidCert  x509.CertificateInvalidError
		recordHeader tls.RecordHeaderError
	)
	return errors.As(err, &addrErr) || errors.As(err, &unknownAuth) ||
		errors.As(err, &hostname) || errors.As(err, &invalidCert) ||
		errors.As(err, &recordHeader)
}

// sleepCtx pauses for d or until ctx is done, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// createEncryptor builds an AES-256-GCM encryptor from the OAuthServerConfig
// encryption key. Returns nil if no key is configured or creation fails.
func createEncryptor(oauthCfg config.OAuthServerConfig) *security.Encryptor {
	if oauthCfg.EncryptionKey == "" {
		return nil
	}
	keyBytes, err := security.DecodeKey(oauthCfg.EncryptionKey)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Failed to decode encryption key for Valkey stores",
			slog.String("error", err.Error()))
		return nil
	}
	enc, err := security.NewEncryptor(keyBytes)
	if err != nil {
		logging.WarnWithAttrs("Aggregator", "Failed to create encryptor for Valkey stores",
			slog.String("error", err.Error()))
		return nil
	}
	if enc.IsEnabled() {
		logging.Info("Aggregator", "Token encryption at rest enabled for Valkey stores (AES-256-GCM)")
	}
	return enc
}

// newValkeyClient creates a valkey.Client from the shared ValkeyConfig. The
// client dials on creation, so an unreachable Valkey is an error here. The
// stores never read through valkey-go's client-side cache (no DoCache), so
// the cache and the CLIENT TRACKING subscription it opens per connection are
// off.
func newValkeyClient(cfg config.ValkeyConfig) (valkey.Client, error) {
	opts := valkey.ClientOption{
		InitAddress:  []string{cfg.URL},
		DisableCache: true,
	}
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	if cfg.DB != 0 {
		opts.SelectDB = cfg.DB
	}
	if cfg.TLSEnabled {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		if cfg.TLSServerName != "" {
			tlsCfg.ServerName = cfg.TLSServerName
		}
		opts.TLSConfig = tlsCfg
	}

	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("valkey connect: %w", err)
	}
	return client, nil
}
