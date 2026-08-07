package authn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTOTPPeriod = 30
	defaultTOTPDigits = 6
	secretByteLen     = 20
)

// GenerateTOTPSecret creates a new random Base32 encoded secret for TOTP.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, secretByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// BuildTOTPUri creates the standard otpauth:// URI for QR code generation in authenticator apps.
func BuildTOTPUri(secret, accountName, issuer string) string {
	if issuer == "" {
		issuer = "ArgusOps"
	}
	label := fmt.Sprintf("%s:%s", issuer, accountName)
	u := url.URL{
		Scheme: "otpauth",
		Host:   "totp",
		Path:   "/" + label,
	}
	q := u.Query()
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", defaultTOTPDigits))
	q.Set("period", fmt.Sprintf("%d", defaultTOTPPeriod))
	u.RawQuery = q.Encode()
	return u.String()
}

// GenerateCode calculates the TOTP code for secret at time t.
func GenerateCode(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}

	counter := uint64(t.Unix() / defaultTOTPPeriod)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	truncatedHash := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7fffffff
	code := truncatedHash % 1000000

	return fmt.Sprintf("%06d", code), nil
}

// VerifyCode verifies code against secret allowing a clock drift window (+/- 1 step).
func VerifyCode(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != defaultTOTPDigits {
		return false
	}

	now := time.Now()
	// Check current time, 30s before, and 30s after for clock drift tolerance
	windows := []time.Time{
		now,
		now.Add(-time.Duration(defaultTOTPPeriod) * time.Second),
		now.Add(time.Duration(defaultTOTPPeriod) * time.Second),
	}

	for _, t := range windows {
		expected, err := GenerateCode(secret, t)
		if err == nil && expected == code {
			return true
		}
	}
	return false
}
