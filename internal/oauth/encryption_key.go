package oauth

import (
	"fmt"

	"github.com/giantswarm/mcp-oauth/security"
)

// DecodeEncryptionKey decodes an AES-256 token-encryption key from either
// base64 (the historical muster format, what `openssl rand -base64 32`
// produces) or hex (`openssl rand -hex 32`). Both forms are accepted so
// operators can use whichever encoding their secret tooling emits.
//
// The format is detected by trying base64 first and falling back to hex.
// mcp-oauth's security.KeyFromBase64 / KeyFromHex both validate that the
// decoded key is exactly 32 bytes; security.NewEncryptor additionally
// rejects low-entropy keys (v0.2.109).
func DecodeEncryptionKey(s string) ([]byte, error) {
	if key, err := security.KeyFromBase64(s); err == nil {
		return key, nil
	}
	key, err := security.KeyFromHex(s)
	if err != nil {
		return nil, fmt.Errorf("encryption key is neither valid base64 nor hex: %w", err)
	}
	return key, nil
}
