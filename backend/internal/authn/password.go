// Package authn implements the mechanics of authentication: password
// hashing, JWT issuance/verification, and the LDAP/SAML federation flows.
// It has no knowledge of HTTP — internal/httpserver/handlers/auth.go calls
// into this package and translates results to responses.
package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters follow the OWASP Password Storage Cheat Sheet
// baseline recommendation (m=19MiB, t=2, p=1) as of this writing — tune
// upward as hardware assumptions change, the encoded hash carries its own
// parameters so changing these never breaks verification of existing hashes.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword returns a self-describing hash string
// ($argon2id$v=19$m=...,t=...,p=...$salt$hash, the same format used by the
// reference argon2 CLI) so VerifyPassword never needs the parameters passed
// separately and old hashes keep verifying after the constants above change.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword parses the parameters back out of encoded (rather than
// assuming today's constants) so a stored hash keeps verifying even after
// argonTime/argonMemory/argonThreads are tuned upward later.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("unrecognized password hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse version: %w", err)
	}

	var memory uint32
	var time_ uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time_, &threads); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}

	got := argon2.IDKey([]byte(password), salt, time_, memory, threads, uint32(len(want)))

	// constant-time comparison: a timing difference here would leak how many
	// leading bytes of the candidate hash matched, letting an attacker brute
	// force the password hash byte by byte
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
