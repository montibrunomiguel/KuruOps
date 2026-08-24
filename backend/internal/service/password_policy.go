package service

import (
	"fmt"
	"unicode"
)

// validatePasswordPolicy is shared by AuthService.ChangePassword and
// PasswordResetService.ConfirmReset -- every path that sets a new password
// enforces the same rule. Length-only (the previous check) accepts
// "11111111"/"aaaaaaaa", both of which pass no real resistance to a
// dictionary or credential-stuffing attempt; requiring at least one letter
// and one digit pushes out the weakest of those without going as far as a
// symbol/complexity requirement, which research (e.g. NIST 800-63B) finds
// mostly just pushes people toward predictable substitutions
// ("Password1!") rather than meaningfully stronger passwords.
func validatePasswordPolicy(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("new password must be at least 8 characters")
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("new password must contain at least one letter and one digit")
	}
	return nil
}
