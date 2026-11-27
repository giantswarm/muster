package config

// RequestStateConfig supplies the root key for HKDF-derived request-state
// sealing, independently of OAuth storage encryption.
type RequestStateConfig struct {
	// KeyFile contains a base64-encoded, high-entropy 32-byte root key.
	KeyFile string `yaml:"keyFile,omitempty"`
	// Key is the resolved file content. It is never serialized into config.
	Key string `yaml:"-"`
}
