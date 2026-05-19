package binary

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Option configures Resolve at call time.
type Option func(*options) error

type options struct {
	baseDir         string
	noDownload      bool
	httpClient      *http.Client
	downloadBaseURL string
	logger          *slog.Logger
}

const defaultDownloadBaseURL = "https://github.com/agentgateway/agentgateway/releases/download"

func defaultOptions() options {
	return options{
		httpClient:      &http.Client{Timeout: 5 * time.Minute},
		downloadBaseURL: defaultDownloadBaseURL,
		logger:          slog.Default(),
	}
}

// WithBaseDir sets the cache root for installed binaries. Empty falls
// back to <user-home>/.config/muster/bin.
func WithBaseDir(dir string) Option {
	return func(o *options) error {
		o.baseDir = dir
		return nil
	}
}

// WithNoDownload disables the network fallback. A cache miss returns
// ErrBinaryNotFound.
func WithNoDownload(b bool) Option {
	return func(o *options) error {
		o.noDownload = b
		return nil
	}
}

// WithHTTPClient overrides the client used for download. nil is rejected.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) error {
		if c == nil {
			return fmt.Errorf("http client must not be nil")
		}
		o.httpClient = c
		return nil
	}
}

// WithDownloadBaseURL overrides the GitHub release URL prefix. Tests
// point this at an httptest.Server. Empty is rejected.
func WithDownloadBaseURL(u string) Option {
	return func(o *options) error {
		if u == "" {
			return fmt.Errorf("download base URL must not be empty")
		}
		o.downloadBaseURL = u
		return nil
	}
}

// WithLogger overrides the slog.Logger used for resolution events. nil
// is rejected.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) error {
		if l == nil {
			return fmt.Errorf("logger must not be nil")
		}
		o.logger = l
		return nil
	}
}
