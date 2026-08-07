package authn_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
)

// generateEd25519 produces a non-RSA keypair so tests can exercise
// LoadPrivateKey/LoadPublicKey's "parsed fine, wrong key type" error path,
// distinct from "failed to parse at all".
func generateEd25519(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	t.Helper()
	return ed25519.GenerateKey(rand.Reader)
}

func TestGenerateEphemeralKeyPair(t *testing.T) {
	key, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	require.NotNil(t, key)
	assert.NoError(t, key.Validate())
	assert.Equal(t, 2048, key.N.BitLen())

	t.Run("two calls produce different keys", func(t *testing.T) {
		key2, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		assert.NotEqual(t, key.N, key2.N)
	})
}

func writePEM(t *testing.T, dir, name string, block *pem.Block) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	return path
}

func TestLoadPrivateKey(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	t.Run("PKCS#1 PEM", func(t *testing.T) {
		path := writePEM(t, dir, "pkcs1.pem", &pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		})
		loaded, err := authn.LoadPrivateKey(path)
		require.NoError(t, err)
		assert.Equal(t, key.N, loaded.N)
	})

	t.Run("PKCS#8 PEM", func(t *testing.T) {
		pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)
		path := writePEM(t, dir, "pkcs8.pem", &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
		loaded, err := authn.LoadPrivateKey(path)
		require.NoError(t, err)
		assert.Equal(t, key.N, loaded.N)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := authn.LoadPrivateKey(filepath.Join(dir, "does-not-exist.pem"))
		assert.Error(t, err)
	})

	t.Run("not a PEM file", func(t *testing.T) {
		path := filepath.Join(dir, "garbage.pem")
		require.NoError(t, os.WriteFile(path, []byte("not pem data"), 0o600))
		_, err := authn.LoadPrivateKey(path)
		assert.Error(t, err)
	})

	t.Run("PEM block that isn't a private key at all", func(t *testing.T) {
		path := writePEM(t, dir, "notakey.pem", &pem.Block{Type: "CERTIFICATE", Bytes: []byte("not actually a key")})
		_, err := authn.LoadPrivateKey(path)
		assert.Error(t, err)
	})

	t.Run("PKCS#8 key that isn't RSA", func(t *testing.T) {
		// x509.MarshalPKCS8PrivateKey only supports RSA/ECDSA/Ed25519 keys
		// in this Go version -- an Ed25519 key exercises the "parsed fine,
		// wrong type" branch distinct from "failed to parse at all".
		_, priv, err := generateEd25519(t)
		require.NoError(t, err)
		pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
		require.NoError(t, err)
		path := writePEM(t, dir, "ed25519.pem", &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
		_, err = authn.LoadPrivateKey(path)
		assert.Error(t, err)
	})
}

func TestLoadPublicKey(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pkix, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	t.Run("valid PKIX PEM", func(t *testing.T) {
		path := writePEM(t, dir, "pub.pem", &pem.Block{Type: "PUBLIC KEY", Bytes: pkix})
		loaded, err := authn.LoadPublicKey(path)
		require.NoError(t, err)
		assert.Equal(t, key.PublicKey.N, loaded.N)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := authn.LoadPublicKey(filepath.Join(dir, "does-not-exist.pem"))
		assert.Error(t, err)
	})

	t.Run("not a PEM file", func(t *testing.T) {
		path := filepath.Join(dir, "garbage-pub.pem")
		require.NoError(t, os.WriteFile(path, []byte("not pem data"), 0o600))
		_, err := authn.LoadPublicKey(path)
		assert.Error(t, err)
	})

	t.Run("PEM block that doesn't parse as PKIX", func(t *testing.T) {
		path := writePEM(t, dir, "badpub.pem", &pem.Block{Type: "PUBLIC KEY", Bytes: []byte("not a real key")})
		_, err := authn.LoadPublicKey(path)
		assert.Error(t, err)
	})

	t.Run("PKIX key that isn't RSA", func(t *testing.T) {
		pub, _, err := generateEd25519(t)
		require.NoError(t, err)
		pkix, err := x509.MarshalPKIXPublicKey(pub)
		require.NoError(t, err)
		path := writePEM(t, dir, "ed25519-pub.pem", &pem.Block{Type: "PUBLIC KEY", Bytes: pkix})
		_, err = authn.LoadPublicKey(path)
		assert.Error(t, err)
	})
}
