package server

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/go-jose/go-jose/v4"
)

// loadSigningKey reads a PEM-encoded private key from path and returns the key,
// its RFC 7638 JWK SHA-256 thumbprint (used as kid), and the matching JWS alg.
// Supported formats: EC PRIVATE KEY (P-256 → ES256), RSA PRIVATE KEY / PRIVATE KEY (PKCS#8).
// Only the first PEM block in the file is used.
func loadSigningKey(path string) (crypto.Signer, string, string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-configured, not user input
	if err != nil {
		return nil, "", "", fmt.Errorf("reading JWT signing key: %w", err)
	}
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, "", "", fmt.Errorf("JWT signing key at %q contains no valid PEM block", path)
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		slog.Warn("JWT signing key file contains multiple PEM blocks; only the first is used", "path", path)
	}

	key, alg, err := parseSigningKeyBlock(block)
	if err != nil {
		return nil, "", "", err
	}

	kid, err := jwkThumbprint(key.Public())
	if err != nil {
		return nil, "", "", fmt.Errorf("computing JWK thumbprint: %w", err)
	}
	return key, kid, alg, nil
}

func parseSigningKeyBlock(block *pem.Block) (crypto.Signer, string, error) {
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parsing EC private key: %w", err)
		}
		if key.Curve != elliptic.P256() {
			return nil, "", fmt.Errorf("EC key uses curve %s; only P-256 (ES256) is supported", key.Curve.Params().Name)
		}
		return key, oauthserver.SigningAlgorithmES256, nil

	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parsing RSA PKCS#1 private key: %w", err)
		}
		if key.N.BitLen() < 2048 {
			return nil, "", fmt.Errorf("RSA key is %d bits; minimum 2048 required for RS256", key.N.BitLen())
		}
		return key, oauthserver.SigningAlgorithmRS256, nil

	case "PRIVATE KEY":
		raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parsing PKCS#8 private key: %w", err)
		}
		switch k := raw.(type) {
		case *ecdsa.PrivateKey:
			if k.Curve != elliptic.P256() {
				return nil, "", fmt.Errorf("EC key uses curve %s; only P-256 (ES256) is supported", k.Curve.Params().Name)
			}
			return k, oauthserver.SigningAlgorithmES256, nil
		case *rsa.PrivateKey:
			if k.N.BitLen() < 2048 {
				return nil, "", fmt.Errorf("RSA key is %d bits; minimum 2048 required for RS256", k.N.BitLen())
			}
			return k, oauthserver.SigningAlgorithmRS256, nil
		default:
			return nil, "", fmt.Errorf("unsupported PKCS#8 key type %T", raw)
		}

	default:
		return nil, "", fmt.Errorf("unsupported PEM block type %q; expected EC PRIVATE KEY, RSA PRIVATE KEY, or PRIVATE KEY", block.Type)
	}
}

// jwkThumbprint returns the RFC 7638 SHA-256 thumbprint of publicKey as a
// base64url-encoded string (no padding), suitable for use as a JWK kid.
func jwkThumbprint(publicKey crypto.PublicKey) (string, error) {
	jwk := jose.JSONWebKey{Key: publicKey}
	raw, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
