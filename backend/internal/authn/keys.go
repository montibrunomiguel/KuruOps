package authn

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// GenerateEphemeralKeyPair creates a throwaway RSA keypair for local
// development when no JWT key files are configured (AUTH_MODE=dev with
// JWT_PRIVATE_KEY_PATH/JWT_PUBLIC_KEY_PATH unset). Tokens signed with it
// don't survive a process restart and no other service could ever verify
// them — never use this outside cmd/api's dev-mode bootstrap.
func GenerateEphemeralKeyPair() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
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
