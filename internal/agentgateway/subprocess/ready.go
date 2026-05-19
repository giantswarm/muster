package subprocess

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// HTTPReadyProbe returns a readiness probe that polls url with GET
// requests until the server responds with 2xx or the context is
// cancelled. interval controls the poll cadence.
//
// Designed for agentgateway's standard readiness endpoint at
// http://localhost:15021/healthz/ready.
func HTTPReadyProbe(url string, interval time.Duration) func(context.Context) error {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	client := &http.Client{Timeout: interval}
	return func(ctx context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var lastErr error
		for {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return fmt.Errorf("build probe request: %w", err)
			}
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return nil
				}
				lastErr = fmt.Errorf("probe %s returned status %d", url, resp.StatusCode)
			} else {
				lastErr = err
			}
			select {
			case <-ctx.Done():
				if lastErr != nil {
					return fmt.Errorf("%w (last probe error: %v)", ctx.Err(), lastErr)
				}
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
}
