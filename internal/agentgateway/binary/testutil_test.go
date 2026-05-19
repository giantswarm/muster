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

func bodyChecksum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// testChecksums returns a pinned-checksum map containing a single entry
// for asset@PinnedVersion whose digest matches body.
func testChecksums(asset string, body []byte) map[string]string {
	return map[string]string{
		asset + "/" + PinnedVersion: bodyChecksum(body),
	}
}

type fixtureServer struct {
	URL       string
	AssetHits *atomic.Int32
}

// serveAsset spins up an httptest.Server that responds with body on the
// asset path under /v<PinnedVersion>/.
func serveAsset(t *testing.T, asset string, body []byte) *fixtureServer {
	t.Helper()
	var assetHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+PinnedVersion+"/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		assetHits.Add(1)
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fixtureServer{URL: srv.URL, AssetHits: &assetHits}
}
