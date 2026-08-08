package authn

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// GenerateEphemeralKeyPair creates a throwaway RSA keypair for local
// development when no JWT key files are configured (AUTH_MODE=dev with
// JWT_PRIVATE_KEY_PATH/JWT_PUBLIC_KEY_PATH unset). Tokens signed with it
// don't survive a process restart and no other service could ever verify
// them — never use this outside cmd/api's dev-mode bootstrap.
//
// cmd/api.loadOrGenerateJWTKeys persists the result via SaveKeyPair so this
// only actually runs once per dev environment, not once per restart --
// generating it here doesn't imply throwing it away after the process
// exits, just that it was never meant to leave this machine.
func GenerateEphemeralKeyPair() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

// SaveKeyPair PEM-encodes key and writes jwt_private.pem/jwt_public.pem into
// dir (created if it doesn't exist), for loadOrGenerateJWTKeys to read back
// on the next process start via LoadPrivateKey/LoadPublicKey -- the dev-mode
// equivalent of running the two `openssl` commands LoadPrivateKey/
// LoadPublicKey's doc comments describe, so a generated dev keypair survives
// a restart instead of invalidating every session on every deploy.
func SaveKeyPair(dir string, key *rsa.PrivateKey) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	// 0600: this is a real signing key, even if only a dev one -- other
	// local users on a shared machine shouldn't be able to read it.
	if err := os.WriteFile(filepath.Join(dir, "jwt_private.pem"), privPEM, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	if err := os.WriteFile(filepath.Join(dir, "jwt_public.pem"), pubPEM, 0o644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}
	return nil
}

// LoadPrivateKey reads a PKCS#1 or PKCS#8 PEM-encoded RSA private key —
// generate one for local dev with:
//
//	openssl genrsa -out jwt_private.pem 2048
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key in %s is not an RSA private key", path)
	}
	return rsaKey, nil
}

// LoadPublicKey reads a PKIX PEM-encoded RSA public key — derive one from
// the matching private key with:
//
//	openssl rsa -in jwt_private.pem -pubout -out jwt_public.pem
func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key in %s is not an RSA public key", path)
	}
	return rsaKey, nil
}
