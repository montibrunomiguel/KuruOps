package authn_test

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"

	"github.com/argusops/argusops/internal/authn"
)

func TestHashPassword_ProducesSelfDescribingHash(t *testing.T) {
	hash, err := authn.HashPassword("correct horse battery staple")
	require.NoError(t, err)
	assert.Regexp(t, `^\$argon2id\$v=\d+\$m=\d+,t=\d+,p=\d+\$[^$]+\$[^$]+$`, hash)
}

func TestHashPassword_DifferentSaltEachCall(t *testing.T) {
	h1, err := authn.HashPassword("same-password")
	require.NoError(t, err)
	h2, err := authn.HashPassword("same-password")
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "two hashes of the same password must differ (random salt)")
}

func TestVerifyPassword_RoundTrip(t *testing.T) {
	hash, err := authn.HashPassword("ChangeMe123!")
	require.NoError(t, err)

	t.Run("correct password verifies", func(t *testing.T) {
		ok, err := authn.VerifyPassword(hash, "ChangeMe123!")
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("wrong password does not verify", func(t *testing.T) {
		ok, err := authn.VerifyPassword(hash, "wrong-password")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("empty password does not verify", func(t *testing.T) {
		ok, err := authn.VerifyPassword(hash, "")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestVerifyPassword_MalformedHash(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"not enough segments", "$argon2id$v=19$m=19456,t=2,p=1$onlysalt"},
		{"wrong algorithm tag", "$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"},
		{"unparsable version", "$argon2id$v=notanumber$m=19456,t=2,p=1$c2FsdA$aGFzaA"},
		{"unparsable params", "$argon2id$v=19$garbage$c2FsdA$aGFzaA"},
		{"invalid base64 salt", "$argon2id$v=19$m=19456,t=2,p=1$not!base64$aGFzaA"},
		{"invalid base64 hash", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$not!base64"},
		{"empty string", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, err := authn.VerifyPassword(c.hash, "anything")
			assert.Error(t, err)
			assert.False(t, ok)
		})
	}
}

func TestVerifyPassword_ToleratesDifferentEncodedParameters(t *testing.T) {
	// VerifyPassword re-parses m/t/p out of the encoded hash rather than
	// assuming today's package constants -- build a hash with deliberately
	// different parameters than authn.HashPassword currently uses (weaker,
	// as an old stored hash might be) and confirm it still verifies. If
	// VerifyPassword ever regressed to hardcoding today's constants instead
	// of reading the encoded ones, this is what would catch it.
	salt := []byte("0123456789abcdef") // 16 bytes, fixed for a deterministic test hash
	const memory, time_, threads = 8 * 1024, 3, 2
	sum := argon2.IDKey([]byte("legacy-password"), salt, time_, memory, threads, 32)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time_, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	)

	ok, err := authn.VerifyPassword(encoded, "legacy-password")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = authn.VerifyPassword(encoded, "wrong-password")
	require.NoError(t, err)
	assert.False(t, ok)
}
