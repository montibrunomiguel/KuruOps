package authn_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
)

func TestIssueVerify_RoundTrip(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)
	verifier := authn.NewVerifier(&priv.PublicKey)

	tenantID, userID := uuid.New(), uuid.New()
	token, err := issuer.Issue(tenantID, userID, true, []string{"alerts", "incidents", "followup"}, []string{"CompanyA"}, true, true)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, tenantID, claims.TenantID)
	assert.Equal(t, userID, claims.UserID)
	assert.True(t, claims.IsAdmin)
	assert.Equal(t, []string{"alerts", "incidents", "followup"}, claims.ResourceAccess)
	assert.Equal(t, []string{"CompanyA"}, claims.AllowedTags)
	assert.True(t, claims.MustChangePassword)
	assert.True(t, claims.MFAEnabled)
}

func TestIssue_EmptyResourceAccessAndTags(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)
	verifier := authn.NewVerifier(&priv.PublicKey)

	token, err := issuer.Issue(uuid.New(), uuid.New(), false, []string{}, nil, false, false)
	require.NoError(t, err)

	claims, err := verifier.Verify(token)
	require.NoError(t, err)
	assert.Empty(t, claims.ResourceAccess)
	assert.Empty(t, claims.AllowedTags)
	assert.False(t, claims.MustChangePassword)
}

func TestVerify_RejectsTokenSignedByADifferentKey(t *testing.T) {
	priv1, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	priv2, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)

	issuer := authn.NewIssuer(priv1)
	wrongVerifier := authn.NewVerifier(&priv2.PublicKey)

	token, err := issuer.Issue(uuid.New(), uuid.New(), true, []string{"alerts"}, nil, false, false)
	require.NoError(t, err)

	_, err = wrongVerifier.Verify(token)
	assert.Error(t, err)
}

func TestVerify_RejectsExpiredToken(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	verifier := authn.NewVerifier(&priv.PublicKey)

	// Build an already-expired token by hand -- Issuer always sets a future
	// expiry, so an expired token can only be produced by controlling
	// ExpiresAt directly, matching what Verify actually needs to reject.
	claims := jwt.MapClaims{
		"tenant_id": uuid.New().String(),
		"user_id":   uuid.New().String(),
		"role":      "admin",
		"exp":       jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		"iat":       jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	require.NoError(t, err)

	_, err = verifier.Verify(signed)
	assert.Error(t, err)
}

func TestVerify_RejectsWrongSigningMethod(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	verifier := authn.NewVerifier(&priv.PublicKey)

	// An HMAC-signed token, even with a "correct"-looking payload, must be
	// rejected outright -- accepting it would let a holder of the PUBLIC
	// key (which is not secret) forge tokens by switching algorithms.
	claims := jwt.MapClaims{
		"tenant_id": uuid.New().String(),
		"user_id":   uuid.New().String(),
		"role":      "admin",
		"exp":       jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("attacker-controlled-secret"))
	require.NoError(t, err)

	_, err = verifier.Verify(signed)
	assert.Error(t, err)
}

func TestVerify_RejectsMalformedToken(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	verifier := authn.NewVerifier(&priv.PublicKey)

	_, err = verifier.Verify("not.a.jwt")
	assert.Error(t, err)

	_, err = verifier.Verify("")
	assert.Error(t, err)
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)
	verifier := authn.NewVerifier(&priv.PublicKey)

	token, err := issuer.Issue(uuid.New(), uuid.New(), false, []string{}, nil, false, false)
	require.NoError(t, err)

	// Flip one character in the middle of the token (payload segment) --
	// the signature was computed over the original bytes, so this must
	// fail verification even though the string still looks token-shaped.
	tampered := []byte(token)
	mid := len(tampered) / 2
	if tampered[mid] == 'a' {
		tampered[mid] = 'b'
	} else {
		tampered[mid] = 'a'
	}

	_, err = verifier.Verify(string(tampered))
	assert.Error(t, err)
}
