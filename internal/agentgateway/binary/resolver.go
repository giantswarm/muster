package binary

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PinnedVersion is the agentgateway release this build of muster
// targets. Bumping it is a deliberate, separate PR — the subprocess
// manager and the native-config schema move in lockstep.
const PinnedVersion = "1.2.1"

// EnvVar names the override environment variable.
const EnvVar = "MUSTER_AGW_BINARY"

// Sentinel errors. Callers use errors.Is to discriminate.
var (
	ErrBinaryNotFound        = errors.New("agentgateway binary not found and download disabled")
	ErrDownloadDisabled      = errors.New("agentgateway binary download disabled by configuration")
	ErrChecksumMismatch      = errors.New("agentgateway binary checksum mismatch")
	ErrUnsupportedPlatform   = errors.New("agentgateway binary unavailable for this platform")
	ErrUnpinnedPlatform      = errors.New("agentgateway binary has no pinned checksum for this platform/version")
	ErrUntrustedDownloadHost = errors.New("agentgateway download base URL host is not on the trust allowlist")
)

// allowedDownloadHosts gates WithDownloadBaseURL. Only github.com and
// loopback (for httptest.Server) may serve the asset.
var allowedDownloadHosts = map[string]struct{}{
	"github.com": {},
	"127.0.0.1":  {},
	"localhost":  {},
	"[::1]":      {},
}

// Resolve returns an absolute path to an executable agentgateway
// binary of the pinned version. See the package doc for the resolution
// order.
func Resolve(ctx context.Context, opts ...Option) (string, error) {
	return resolveWithChecksums(ctx, pinnedChecksums, opts...)
}

func resolveWithChecksums(ctx context.Context, checksums map[string]string, opts ...Option) (string, error) {
	cfg := defaultOptions()
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return "", fmt.Errorf("apply option: %w", err)
		}
	}
	if cfg.baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine user home: %w", err)
		}
		cfg.baseDir = filepath.Join(home, ".config", "muster", "bin")
	}
	if err := checkDownloadHost(cfg.downloadBaseURL); err != nil {
		return "", err
	}

	if envPath := os.Getenv(EnvVar); envPath != "" {
		if err := checkExecutable(envPath); err != nil {
			return "", fmt.Errorf("%s=%s: %w", EnvVar, envPath, err)
		}
		cfg.logger.Info("resolved agentgateway binary", "source", "env", "path", envPath)
		return envPath, nil
	}

	cachedPath := filepath.Join(cfg.baseDir, cacheFilename(runtime.GOOS))
	if err := checkExecutable(cachedPath); err == nil {
		cfg.logger.Info("resolved agentgateway binary", "source", "cache", "path", cachedPath)
		return cachedPath, nil
	}

	if cfg.noDownload {
		return "", fmt.Errorf("%w: cache %s missing", ErrBinaryNotFound, cachedPath)
	}

	asset, err := assetForPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	expected, ok := checksums[asset+"/"+PinnedVersion]
	if !ok {
		return "", fmt.Errorf("%w: %s v%s", ErrUnpinnedPlatform, asset, PinnedVersion)
	}
	if err := download(ctx, cfg, asset, expected, cachedPath); err != nil {
		return "", err
	}
	cfg.logger.Info("resolved agentgateway binary", "source", "download", "path", cachedPath)
	return cachedPath, nil
}

// checkExecutable returns nil iff path is a non-empty regular file with
// at least one execute bit set. On Windows the execute bit is not checked;
// existence is sufficient because the OS keys off the .exe extension.
// Zero-byte files are rejected on every platform — a truncated download
// or a touch'd placeholder passes the existence and exec-bit checks but
// fails to run.
func checkExecutable(path string) error {
	info, err := os.Stat(path) //nolint:gosec // path is env-var override or constructed cache filename, both caller-trusted
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if info.Size() == 0 {
		return fmt.Errorf("is a zero-byte file")
	}
	if runtime.GOOS != goosWindows && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("not executable (mode %v)", info.Mode().Perm())
	}
	return nil
}

// checkDownloadHost rejects download base URLs whose scheme is not
// http(s) or whose host is not on allowedDownloadHosts. The port is
// not part of the match.
func checkDownloadHost(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse download base URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%w: scheme %q", ErrUntrustedDownloadHost, parsed.Scheme)
	}
	host := parsed.Hostname()
	if _, ok := allowedDownloadHosts[host]; !ok {
		return fmt.Errorf("%w: %q", ErrUntrustedDownloadHost, host)
	}
	return nil
}

// TODO(security): once agentgateway publishes cosign signatures with
// per-asset attestations, verify those instead of the in-source pinned
// checksums and lift WithDownloadBaseURL's host allowlist.
func download(ctx context.Context, cfg options, asset, expected, dest string) error {
	if err := os.MkdirAll(cfg.baseDir, 0o755); err != nil { //nolint:gosec // 0o755 lets siblings read/execute the cached binary
		return fmt.Errorf("create base dir %s: %w", cfg.baseDir, err)
	}
	assetURL := fmt.Sprintf("%s/v%s/%s", cfg.downloadBaseURL, PinnedVersion, asset)

	cfg.logger.Info("downloading agentgateway", "asset", asset, "url", assetURL)

	tmpPath, actual, err := streamToTempFile(ctx, cfg.httpClient, assetURL, cfg.baseDir)
	if err != nil {
		return fmt.Errorf("download %s: %w", assetURL, err)
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if actual != expected {
		return fmt.Errorf("%w: asset %s digest %s, expected %s", ErrChecksumMismatch, asset, actual, expected)
	}

	if runtime.GOOS != goosWindows {
		if err := os.Chmod(tmpPath, 0o755); err != nil { //nolint:gosec // executable bit required for the cached binary
			return fmt.Errorf("chmod temp: %w", err)
		}
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("install %s: %w", dest, err)
	}
	return nil
}

// parseDigest extracts the SHA-256 hex digest from a sha256sum-format
// body. The body must contain exactly one non-empty line, that line
// must carry both a 64-char lowercase hex digest and a filename column
// (basename-matched against expectedFilename, leading `*` stripped).
func parseDigest(body, expectedFilename string) (string, error) {
	var found string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", fmt.Errorf("%w: line missing filename column: %q", ErrChecksumMismatch, line)
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != 64 {
			return "", fmt.Errorf("%w: digest length %d != 64", ErrChecksumMismatch, len(digest))
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("%w: digest not hex: %v", ErrChecksumMismatch, err)
		}
		name := strings.TrimPrefix(fields[1], "*")
		name = filepath.Base(name)
		if name != expectedFilename {
			return "", fmt.Errorf("%w: filename %q does not match expected %q", ErrChecksumMismatch, name, expectedFilename)
		}
		if found != "" {
			return "", fmt.Errorf("%w: input contains more than one hash line", ErrChecksumMismatch)
		}
		found = digest
	}
	if found == "" {
		return "", fmt.Errorf("%w: no valid hash line for %q", ErrChecksumMismatch, expectedFilename)
	}
	return found, nil
}

func streamToTempFile(ctx context.Context, client *http.Client, url, baseDir string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("http %d", resp.StatusCode)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", "", fmt.Errorf("random suffix: %w", err)
	}
	tmpPath := filepath.Join(baseDir, fmt.Sprintf(".agentgateway-v%s.tmp.%d.%s", PinnedVersion, os.Getpid(), hex.EncodeToString(suffix)))

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // tmpPath is constructed from caller-supplied baseDir plus a pid+random suffix
	if err != nil {
		return "", "", fmt.Errorf("create temp %s: %w", tmpPath, err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("stream body: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("close temp: %w", err)
	}
	return tmpPath, hex.EncodeToString(hasher.Sum(nil)), nil
}
