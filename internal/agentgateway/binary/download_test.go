package binary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolve_DownloadHappy(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	asset := runtimeAsset(t)
	body := []byte(synthBody)
	srv := serveAsset(t, asset, body)

	got, err := resolveWithChecksums(t.Context(), testChecksums(asset, body),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(baseDir, cacheFilename(runtime.GOOS)), got)

	data, err := os.ReadFile(got) //nolint:gosec // path returned from Resolve into t.TempDir()-derived baseDir
	require.NoError(t, err)
	require.Equal(t, synthBody, string(data))

	if runtime.GOOS != goosWindows {
		info, err := os.Stat(got)
		require.NoError(t, err)
		require.NotZero(t, info.Mode().Perm()&0o111, "installed binary must be executable")
	}

	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".tmp.", "temp file leaked: %s", e.Name())
	}
}

func TestResolve_ConstantChecksumVerification(t *testing.T) {
	asset := runtimeAsset(t)
	body := []byte(synthBody)

	t.Run("constant matches", func(t *testing.T) {
		t.Setenv(EnvVar, "")
		baseDir := t.TempDir()
		srv := serveAsset(t, asset, body)
		got, err := resolveWithChecksums(t.Context(), testChecksums(asset, body),
			WithBaseDir(baseDir),
			WithDownloadBaseURL(srv.URL),
		)
		require.NoError(t, err)
		require.FileExists(t, got)
		require.Equal(t, int32(1), srv.AssetHits.Load())
	})

	t.Run("constant mismatches", func(t *testing.T) {
		t.Setenv(EnvVar, "")
		baseDir := t.TempDir()
		srv := serveAsset(t, asset, body)
		bogus := map[string]string{
			asset + "/" + PinnedVersion: strings.Repeat("0", 64),
		}
		_, err := resolveWithChecksums(t.Context(), bogus,
			WithBaseDir(baseDir),
			WithDownloadBaseURL(srv.URL),
		)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrChecksumMismatch), "got %v", err)
		_, statErr := os.Stat(filepath.Join(baseDir, cacheFilename(runtime.GOOS)))
		require.ErrorIs(t, statErr, os.ErrNotExist, "no cache file on mismatch")
		matches, err := filepath.Glob(filepath.Join(baseDir, "*.tmp*"))
		require.NoError(t, err)
		require.Empty(t, matches, "temp file leaked on mismatch")
	})

	t.Run("unpinned platform fails closed", func(t *testing.T) {
		t.Setenv(EnvVar, "")
		baseDir := t.TempDir()
		srv := serveAsset(t, asset, body)
		_, err := resolveWithChecksums(t.Context(), map[string]string{},
			WithBaseDir(baseDir),
			WithDownloadBaseURL(srv.URL),
		)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrUnpinnedPlatform), "got %v", err)
		require.Zero(t, srv.AssetHits.Load(), "unpinned platform must not trigger network access")
	})
}

func TestResolve_ChecksumMismatch(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	asset := runtimeAsset(t)
	srv := serveAsset(t, asset, []byte(synthBody))

	bogus := map[string]string{
		asset + "/" + PinnedVersion: strings.Repeat("0", 64),
	}
	_, err := resolveWithChecksums(t.Context(), bogus,
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrChecksumMismatch), "got %v", err)
	_, statErr := os.Stat(filepath.Join(baseDir, cacheFilename(runtime.GOOS)))
	require.ErrorIs(t, statErr, os.ErrNotExist, "no cache file on mismatch")
}

func TestResolve_BaseDirReadOnlyParent(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("chmod 0500 semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission bits")
	}
	t.Setenv(EnvVar, "")
	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0o500))       //nolint:gosec // intentional read-only-parent fault injection
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) //nolint:gosec // restore so t.TempDir cleanup can rm
	baseDir := filepath.Join(parent, "bin")
	asset := runtimeAsset(t)
	body := []byte(synthBody)
	srv := serveAsset(t, asset, body)

	_, err := resolveWithChecksums(t.Context(), testChecksums(asset, body),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create base dir")
}

func TestResolve_ContextCanceledMidDownload(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	asset := runtimeAsset(t)

	started := make(chan struct{})
	var startOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+PinnedVersion+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		<-started
		cancel()
	}()

	_, err := resolveWithChecksums(ctx, testChecksums(asset, []byte(synthBody)),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "got %v", err)

	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".tmp.", "temp file leaked: %s", e.Name())
	}
	_, statErr := os.Stat(filepath.Join(baseDir, cacheFilename(runtime.GOOS)))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestResolve_UpstreamHTTP404(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	asset := runtimeAsset(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+PinnedVersion+"/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, err := resolveWithChecksums(t.Context(), testChecksums(asset, []byte(synthBody)),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
}

func TestResolve_UpstreamHTTP500(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	asset := runtimeAsset(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+PinnedVersion+"/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, err := resolveWithChecksums(t.Context(), testChecksums(asset, []byte(synthBody)),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestResolve_Concurrent(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	asset := runtimeAsset(t)
	body := []byte(synthBody)
	srv := serveAsset(t, asset, body)
	checksums := testChecksums(asset, body)

	const n = 8
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p, err := resolveWithChecksums(t.Context(), checksums,
				WithBaseDir(baseDir),
				WithDownloadBaseURL(srv.URL),
			)
			paths[idx] = p
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i], "goroutine %d", i)
	}
	for i := 1; i < n; i++ {
		require.Equal(t, paths[0], paths[i])
	}
	data, err := os.ReadFile(paths[0]) //nolint:gosec // path returned from Resolve
	require.NoError(t, err)
	require.Equal(t, synthBody, string(data))

	matches, err := filepath.Glob(filepath.Join(baseDir, "*.tmp*"))
	require.NoError(t, err)
	require.Empty(t, matches, "leftover temp files: %v", matches)
}

func TestResolve_DefaultBaseDirUsesHome(t *testing.T) {
	t.Setenv(EnvVar, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	asset := runtimeAsset(t)
	body := []byte(synthBody)
	srv := serveAsset(t, asset, body)

	got, err := resolveWithChecksums(t.Context(), testChecksums(asset, body), WithDownloadBaseURL(srv.URL))
	require.NoError(t, err)
	wantPrefix := filepath.Join(home, ".config", "muster", "bin") + string(filepath.Separator)
	require.True(t, strings.HasPrefix(got, wantPrefix), "got %q want prefix %q", got, wantPrefix)
}
