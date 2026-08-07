package authn

import (
	"testing"
	"time"
)

func TestTOTP_GenerateAndVerify(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("unexpected error generating secret: %v", err)
	}
	if len(secret) == 0 {
		t.Fatal("expected non-empty secret")
	}

	uri := BuildTOTPUri(secret, "admin@argusops.local", "ArgusOps")
	if uri == "" {
		t.Fatal("expected non-empty otpauth uri")
	}

	code, err := GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("unexpected error generating code: %v", err)
	}

	if !VerifyCode(secret, code) {
		t.Fatalf("expected code %s to be valid for secret %s", code, secret)
	}

	if VerifyCode(secret, "000000") && code != "000000" {
		t.Fatal("expected invalid code to fail verification")
	}
}
