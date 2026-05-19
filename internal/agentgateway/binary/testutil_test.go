package binary

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
)

const synthBody = "not-a-real-agw-binary-payload\n"

func runtimeAsset(t *testing.T) string {
	t.Helper()
	asset, err := assetForPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("runtime platform %s/%s not in resolver matrix", runtime.GOOS, runtime.GOARCH)
	}
	return asset
}

type fixtureServer struct {
	URL       string
	AssetHits *atomic.Int32
	SumHits   *atomic.Int32
}

// serveAsset spins up an httptest.Server that responds with body for
// the asset path and a matching SHA256 (with the upstream "outputs/"
// filename prefix) for the .sha256 path.
func serveAsset(t *testing.T, asset string, body []byte) *fixtureServer {
	t.Helper()
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:]) + "  outputs/" + asset + "\n"
	return serveAssetCustomSum(t, asset, body, sha)
}

func serveAssetCustomSum(t *testing.T, asset string, body []byte, sumBody string) *fixtureServer {
	t.Helper()
	var assetHits, sumHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+PinnedVersion+"/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		assetHits.Add(1)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/v"+PinnedVersion+"/"+asset+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		sumHits.Add(1)
		_, _ = w.Write([]byte(sumBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fixtureServer{URL: srv.URL, AssetHits: &assetHits, SumHits: &sumHits}
}
