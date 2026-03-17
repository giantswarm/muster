package oauth

import "github.com/giantswarm/mcp-oauth/security"

// valkeyEncryption wraps a security.Encryptor to provide consistent
// encrypt/decrypt helpers shared by ValkeyTokenStore and ValkeyStateStore.
type valkeyEncryption struct {
	encryptor *security.Encryptor
}

func (e *valkeyEncryption) encryptValue(data []byte) (string, error) {
	if e.encryptor == nil || !e.encryptor.IsEnabled() {
		return string(data), nil
	}
	return e.encryptor.Encrypt(string(data))
}

func (e *valkeyEncryption) decryptValue(stored string) ([]byte, error) {
	if e.encryptor == nil || !e.encryptor.IsEnabled() {
		return []byte(stored), nil
	}
	plaintext, err := e.encryptor.Decrypt(stored)
	if err != nil {
		return nil, err
	}
	return []byte(plaintext), nil
}
