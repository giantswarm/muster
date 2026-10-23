package oauth

import (
	"fmt"
	"os"
	"path/filepath"
)

// LockRefresh serializes token refreshes for serverURL across every muster
// process sharing this store's directory. The refresh token rotates on every
// refresh and the server answers a rotated one with OAuth 2.1 reuse
// detection: it revokes the whole grant, which signs the person out of every
// client. A process holding the lock re-reads the token file (ReloadToken)
// before sending its refresh token, so it never sends one another process has
// already spent. The returned func releases the lock; the operating system
// releases it too should the process end. A memory-only store has nothing to
// share and returns a no-op.
func (s *TokenStore) LockRefresh(serverURL string) (func(), error) {
	if !s.fileMode {
		return func() {}, nil
	}

	path := filepath.Join(s.storageDir, s.tokenKey(serverURL)+".lock")
	// #nosec G304 -- path is constructed from internal key, not user input
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open token refresh lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to take token refresh lock: %w", err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// ReloadToken re-reads the token for serverURL from its file, replacing this
// process's cached copy, and returns it even when it is expiring. Another
// process may have refreshed the token since this one cached it. A
// memory-only store, or a missing file, returns the cached token.
func (s *TokenStore) ReloadToken(serverURL string) *StoredToken {
	key := s.tokenKey(serverURL)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fileMode {
		if token, err := s.readTokenFile(key); err == nil {
			s.tokens[key] = token
			return token
		}
	}
	return s.tokens[key]
}
