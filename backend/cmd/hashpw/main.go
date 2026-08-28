// Command hashpw prints an argon2id hash for a password, in the same
// $argon2id$... format internal/authn/password.go verifies against. There
// is no signup/admin-creation endpoint yet (see backend/README.md), so this
// is how a local user row's password_hash gets seeded — by hand, or from
// scripts/smoke-test.sh.
//
// Usage: go run ./cmd/hashpw 'the password'
package main

import (
	"fmt"
	"os"

	"github.com/kuruops/kuruops/internal/authn"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hashpw <password>")
		os.Exit(1)
	}
	hash, err := authn.HashPassword(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}
	fmt.Println(hash)
}
