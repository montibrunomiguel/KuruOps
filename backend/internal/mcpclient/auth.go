package mcpclient

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Auth is the one credential header sent on every request to an MCP server.
// The zero value means no authentication. Whatever the configured auth type
// -- bearer token, API key, or an OAuth access token fetched beforehand --
// it all reduces to "set this header to this value" by the time a request is
// built, which keeps Client itself ignorant of how a credential was obtained.
type Auth struct {
	Header string
	Value  string
}

// Bearer is "Authorization: Bearer <token>". An empty token yields the zero
// Auth (no header), not a literal "Bearer " with nothing after it.
func Bearer(token string) Auth {
	if token == "" {
		return Auth{}
	}
	return Auth{Header: "Authorization", Value: "Bearer " + token}
}

// Header is an API-key style credential: an arbitrary header name carrying
// the key verbatim.
func Header(name, value string) Auth {
	if name == "" || value == "" {
		return Auth{}
	}
	return Auth{Header: name, Value: value}
}

func (a Auth) apply(h http.Header) {
	if a.Header != "" && a.Value != "" {
		h.Set(a.Header, a.Value)
	}
}

// reservedHeaders are request headers an API-key header name must not be:
// ones this client sets itself (so the key would be silently overwritten or
// would clobber protocol behavior), plus framing and hop-by-hop headers, where
// letting an admin-chosen value through is a request-smuggling footgun
// rather than an authentication scheme. Lower-cased for comparison.
var reservedHeaders = map[string]struct{}{
	"host": {}, "content-length": {}, "content-type": {}, "accept": {},
	"mcp-session-id": {}, "transfer-encoding": {}, "connection": {},
	"keep-alive": {}, "upgrade": {}, "te": {}, "trailer": {},
	"proxy-authorization": {}, "proxy-authenticate": {}, "expect": {},
}

// ValidateHeaderName reports whether name is usable as the header an API key
// is sent in: a non-empty RFC 7230 "token" (letters, digits and !#$%&'*+-.^_`|~
// only -- no spaces, colons, or control characters that could split or
// inject headers) that isn't one of reservedHeaders.
func ValidateHeaderName(name string) error {
	if name == "" {
		return errors.New("header name is required")
	}
	if len(name) > 128 {
		return errors.New("header name is too long")
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return fmt.Errorf("header name %q contains an invalid character", name)
		}
	}
	if _, reserved := reservedHeaders[strings.ToLower(name)]; reserved {
		return fmt.Errorf("header name %q is reserved and can't carry a credential", name)
	}
	return nil
}

func isTokenChar(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

const maxCredentialLen = 4096

// ValidateCredential rejects a credential value that is empty, oversized, or
// contains a control character. The last one is the one that matters for
// security: a CR/LF in a token pasted into a header value is a header-injection
// vector, and a stray newline from a copy-paste is by far the most common way
// an otherwise-valid token breaks authentication. Callers trim surrounding
// whitespace first.
func ValidateCredential(value string) error {
	if value == "" {
		return errors.New("value is required")
	}
	if len(value) > maxCredentialLen {
		return fmt.Errorf("value is longer than %d characters", maxCredentialLen)
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c == 0x7f {
			return errors.New("value contains a control character")
		}
	}
	return nil
}
