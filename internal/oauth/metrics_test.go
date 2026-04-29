package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestTokenExchangeMetrics_ValidationError asserts result="error" is recorded
// when the request fails request-level validation (no network call). The
// pre-flight validation path is the cheapest way to exercise the error label
// without a real Dex.
func TestTokenExchangeMetrics_ValidationError(t *testing.T) {
	resetTokenExchangeMetricsForTest()
	t.Cleanup(resetTokenExchangeMetricsForTest)

	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{})

	// nil request triggers the validateExchangeRequest error path.
	if _, err := exchanger.Exchange(context.Background(), nil); err == nil {
		t.Fatal("expected validation error for nil request")
	}

	expected := `
# HELP muster_token_exchange_total Number of RFC 8693 token-exchange invocations, by outcome.
# TYPE muster_token_exchange_total counter
muster_token_exchange_total{result="error"} 1
`
	if err := testutil.CollectAndCompare(tokenExchangeTotal, strings.NewReader(expected)); err != nil {
		t.Fatalf("metric mismatch: %v", err)
	}
}

// TestTokenExchangeMetrics_SuccessAndCacheHit drives Exchange against an
// httptest TLS server simulating a Dex /token endpoint and asserts the
// success → cache_hit label progression on a second identical call.
func TestTokenExchangeMetrics_SuccessAndCacheHit(t *testing.T) {
	resetTokenExchangeMetricsForTest()
	t.Cleanup(resetTokenExchangeMetricsForTest)

	// Minimal Dex stub: returns a JSON token-exchange response on /token.
	dex := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":      "exchanged-token",
			"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
			"token_type":        "Bearer",
			"expires_in":        300,
		})
	}))
	t.Cleanup(dex.Close)

	// Pin the exchanger's HTTP client to the test server's certificate so the
	// HTTPS-only validation passes.
	pool := x509.NewCertPool()
	pool.AddCert(dex.Certificate())
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{
		HTTPClient:     httpClient,
		AllowPrivateIP: true, // httptest binds to 127.0.0.1
	})

	req := &ExchangeRequest{
		Config: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: dex.URL + "/token",
			ConnectorID:      "giantswarm",
		},
		SubjectToken: "subject-token",
		UserID:       "user-1",
	}

	// First call: cache miss → "success".
	if _, err := exchanger.Exchange(context.Background(), req); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	// Second call: cache hit.
	if _, err := exchanger.Exchange(context.Background(), req); err != nil {
		t.Fatalf("second exchange: %v", err)
	}

	expected := `
# HELP muster_token_exchange_total Number of RFC 8693 token-exchange invocations, by outcome.
# TYPE muster_token_exchange_total counter
muster_token_exchange_total{result="cache_hit"} 1
muster_token_exchange_total{result="success"} 1
`
	if err := testutil.CollectAndCompare(tokenExchangeTotal, strings.NewReader(expected)); err != nil {
		t.Fatalf("metric mismatch: %v", err)
	}
}
