package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolve_EnvVarHappy(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "agw-fake")
	require.NoError(t, os.WriteFile(envPath, []byte("fake"), 0o755)) //nolint:gosec // test fake; needs exec bit
	t.Setenv(EnvVar, envPath)

	srv := serveAsset(t, runtimeAsset(t), []byte(synthBody))
	got, err := Resolve(t.Context(),
		WithBaseDir(t.TempDir()),
		WithDownloadBaseURL(srv.URL),
	)
	require.NoError(t, err)
	require.Equal(t, envPath, got)
	require.Zero(t, srv.AssetHits.Load(), "env path must not trigger download")
}

func TestResolve_EnvVarBrokenPath(t *testing.T) {
	t.Setenv(EnvVar, "/does/not/exist/agw")
	_, err := Resolve(t.Context(), WithBaseDir(t.TempDir()), WithNoDownload(true))
	require.Error(t, err)
	require.Contains(t, err.Error(), EnvVar)
	require.Contains(t, err.Error(), "/does/not/exist/agw")
	require.False(t, errors.Is(err, ErrBinaryNotFound), "env error must not fall through to cache miss")
}

func TestResolve_EnvVarNonExecutable(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("permission bits don't gate executability on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "agw")
	require.NoError(t, os.WriteFile(p, []byte("data"), 0o644)) //nolint:gosec // intentionally non-executable to exercise the check
	t.Setenv(EnvVar, p)
	_, err := Resolve(t.Context(), WithBaseDir(t.TempDir()), WithNoDownload(true))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not executable")
}

func TestResolve_EnvVarPointsAtDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, dir)
	_, err := Resolve(t.Context(), WithBaseDir(t.TempDir()), WithNoDownload(true))
	require.Error(t, err, "a directory at MUSTER_AGW_BINARY must be rejected")
	require.Contains(t, err.Error(), EnvVar)
}

func TestResolve_EnvVarPointsAtEmptyFile(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("Windows skips the size check today; tracked for a follow-up")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "agw")
	require.NoError(t, os.WriteFile(p, []byte{}, 0o755)) //nolint:gosec // executable bit needed; size==0 is the test condition
	t.Setenv(EnvVar, p)
	_, err := Resolve(t.Context(), WithBaseDir(t.TempDir()), WithNoDownload(true))
	require.Error(t, err, "a zero-byte file at MUSTER_AGW_BINARY must be rejected")
	require.Contains(t, err.Error(), EnvVar)
}

func TestResolve_CacheHit(t *testing.T) {
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	cached := filepath.Join(baseDir, cacheFilename(runtime.GOOS))
	require.NoError(t, os.WriteFile(cached, []byte("cached-bytes"), 0o755)) //nolint:gosec // simulating installed binary

	srv := serveAsset(t, runtimeAsset(t), []byte(synthBody))
	got, err := Resolve(t.Context(),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.NoError(t, err)
	require.Equal(t, cached, got)
	require.Zero(t, srv.AssetHits.Load(), "cache hit must not trigger download")
}

func TestResolve_CacheNonExecutableRedownloads(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("permission bits don't gate executability on Windows")
	}
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	cached := filepath.Join(baseDir, cacheFilename(runtime.GOOS))
	require.NoError(t, os.WriteFile(cached, []byte("stale"), 0o644)) //nolint:gosec // stale non-executable forces re-download

	asset := runtimeAsset(t)
	body := []byte(synthBody)
	srv := serveAsset(t, asset, body)
	got, err := resolveWithChecksums(t.Context(), testChecksums(asset, body),
		WithBaseDir(baseDir),
		WithDownloadBaseURL(srv.URL),
	)
	require.NoError(t, err)
	require.Equal(t, cached, got)
	require.Equal(t, int32(1), srv.AssetHits.Load())
	data, err := os.ReadFile(cached) //nolint:gosec // path is t.TempDir()-derived
	require.NoError(t, err)
	require.Equal(t, synthBody, string(data))
}

func TestResolve_CacheNonExecutableNoDownloadErrors(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("permission bits don't gate executability on Windows")
	}
	t.Setenv(EnvVar, "")
	baseDir := t.TempDir()
	cached := filepath.Join(baseDir, cacheFilename(runtime.GOOS))
	require.NoError(t, os.WriteFile(cached, []byte("stale"), 0o644)) //nolint:gosec // stale non-executable

	_, err := Resolve(t.Context(),
		WithBaseDir(baseDir),
		WithNoDownload(true),
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrBinaryNotFound))
}

func TestResolve_NoDownloadCacheMiss(t *testing.T) {
	t.Setenv(EnvVar, "")
	_, err := Resolve(t.Context(),
		WithBaseDir(t.TempDir()),
		WithNoDownload(true),
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrBinaryNotFound))
}

func TestResolve_OptionValidation(t *testing.T) {
	t.Setenv(EnvVar, "")
	_, err := Resolve(t.Context(), WithHTTPClient(nil))
	require.Error(t, err)
	_, err = Resolve(t.Context(), WithDownloadBaseURL(""))
	require.Error(t, err)
	_, err = Resolve(t.Context(), WithLogger(nil))
	require.Error(t, err)
}

func TestResolve_UntrustedDownloadHost(t *testing.T) {
	t.Setenv(EnvVar, "")
	cases := []string{
		"https://evil.example.com/releases/download",
		"https://github.com.evil.example/releases/download",
		"ftp://github.com/releases/download",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := Resolve(t.Context(),
				WithBaseDir(t.TempDir()),
				WithDownloadBaseURL(raw),
				WithNoDownload(true),
			)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrUntrustedDownloadHost), "got %v", err)
		})
	}
}

func TestParseDigest(t *testing.T) {
	good := strings.Repeat("a", 64)
	const filename = "agentgateway-linux-amd64"
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"with filename", good + "  " + filename + "\n", good, false},
		{"upstream outputs prefix", good + "  outputs/" + filename + "\n", good, false},
		{"binary mode star", good + " *outputs/" + filename + "\n", good, false},
		{"uppercase", strings.ToUpper(good) + "  " + filename + "\n", good, false},
		{"empty", "", "", true},
		{"short", "deadbeef foo\n", "", true},
		{"non-hex", strings.Repeat("z", 64) + "  " + filename + "\n", "", true},
		{"hex only no filename", good + "\n", "", true},
		{"wrong filename", good + "  other-asset\n", "", true},
		{"multi line two valid", good + "  " + filename + "\n" + strings.Repeat("b", 64) + "  " + filename + "\n", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDigest(tc.in, filename)
			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, ErrChecksumMismatch), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDefaultOptionsValid(t *testing.T) {
	cfg := defaultOptions()
	require.NotNil(t, cfg.httpClient)
	require.NotEmpty(t, cfg.downloadBaseURL)
	require.NotNil(t, cfg.logger)
	require.Equal(t, defaultDownloadBaseURL, cfg.downloadBaseURL)
}

func TestPinnedVersionFormat(t *testing.T) {
	parts := strings.Split(PinnedVersion, ".")
	require.Len(t, parts, 3, "expected MAJOR.MINOR.PATCH, got %q", PinnedVersion)
	for _, p := range parts {
		_, err := fmt.Sscanf(p, "%d", new(int))
		require.NoError(t, err, "non-numeric component %q in %q", p, PinnedVersion)
	}
}

func TestPinnedChecksumsCoverAllPlatforms(t *testing.T) {
	for plat, asset := range supportedAssets {
		_, ok := pinnedChecksums[asset]
		require.True(t, ok, "missing pinned checksum for %s/%s asset %q at v%s", plat.os, plat.arch, asset, PinnedVersion)
	}
}
