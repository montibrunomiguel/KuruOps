package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// generatePrefixedToken and hashToken back every bearer-token family this
// package issues (refresh tokens, password-reset tokens, webhook tokens,
// personal API tokens) -- previously each had its own copy of this exact
// random-bytes/base64url/sha256-hex logic, one per service, with a
// comment in each pointing at the others as precedent. Consolidated here
// so a future change (e.g. more entropy, a different hash) only has to
// happen once.

// generatePrefixedToken returns prefix followed by the base64url encoding
// of nBytes cryptographically random bytes -- the plaintext token, shown to
// the caller exactly once at issuance/rotation and never retrievable again.
func generatePrefixedToken(prefix string, nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken is the one-way digest stored in place of a plaintext token --
// every token family compares an incoming bearer token by hashing it and
// looking up the hash, never storing or comparing plaintext.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
