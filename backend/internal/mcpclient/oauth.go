package mcpclient

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kuruops/kuruops/internal/httpguard"
)

// OAuthConfig is an OAuth 2.0 client_credentials registration (RFC 6749 §4.4):
// the machine-to-machine grant, which is the right one for a backend service
// calling an MCP server -- there is no end user to redirect through a browser.
// TokenURL is the authorization server's token endpoint.
type OAuthConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
}

const (
	tokenRequestTimeout = 15 * time.Second
	// Responses from a token endpoint are tiny JSON documents; anything bigger
	// is either a misconfigured URL (an HTML page) or hostile.
	maxTokenResponseBytes = 1 << 20
	// A token endpoint that omits expires_in leaves the lifetime undefined
	// (RFC 6749 §5.1 only says the server SHOULD provide it). Assume short.
	defaultTokenLifetime = 5 * time.Minute
	// Refresh this long before the real expiry so a request never goes out
	// carrying a token that expires in flight.
	tokenExpirySkew = 30 * time.Second
	// Never cache longer than this, whatever expires_in claims: it bounds how
	// long a revoked credential keeps working from our cache.
	maxTokenCacheTTL = time.Hour
)

type tokenResponse struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	ExpiresIn   json.Number `json:"expires_in"`
	Error       string      `json:"error"`
}

// fetchClientCredentialsToken exchanges the client's credentials for an access
// token. It returns the token and its remaining lifetime.
//
// Security notes, since this sends a long-lived secret to an admin-supplied URL:
//   - the request goes through httpguard's client, so the token endpoint can't
//     be pointed at loopback / link-local / private addresses (SSRF);
//   - redirects are never followed -- the Basic credentials would otherwise be
//     replayed to wherever the endpoint says to go;
//   - the client authenticates with HTTP Basic (client_secret_basic), the method
//     RFC 6749 §2.3.1 requires servers to support, so the secret is never in a
//     URL or request body that a proxy or access log might record;
//   - error values carry only the status code and the standard OAuth `error`
//     code, never the response body, which could echo credentials and ends up
//     persisted in ai_tool_calls.result via MCPToolService.recordFailure.
func fetchClientCredentialsToken(ctx context.Context, cfg OAuthConfig) (string, time.Duration, error) {
	client := httpguard.NewClient(tokenRequestTimeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, errors.New("oauth: invalid token URL")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// RFC 6749 §2.3.1: the id and secret are form-urlencoded *before* being
	// base64'd into the Basic header, so a secret containing ':' or '%' survives.
	basic := url.QueryEscape(cfg.ClientID) + ":" + url.QueryEscape(cfg.ClientSecret)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(basic)))

	resp, err := client.Do(req)
	if err != nil {
		// Deliberately not %w of the transport error: net/url errors embed the
		// full request URL.
		return "", 0, errors.New("oauth: token request failed")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return "", 0, errors.New("oauth: reading token response failed")
	}
	var tr tokenResponse
	jsonErr := json.Unmarshal(body, &tr)

	if resp.StatusCode != http.StatusOK {
		if jsonErr == nil && isSafeErrorCode(tr.Error) {
			return "", 0, fmt.Errorf("oauth: token endpoint returned %d (%s)", resp.StatusCode, tr.Error)
		}
		return "", 0, fmt.Errorf("oauth: token endpoint returned %d", resp.StatusCode)
	}
	if jsonErr != nil {
		return "", 0, errors.New("oauth: token endpoint did not return JSON")
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "bearer") {
		return "", 0, fmt.Errorf("oauth: unsupported token_type %q", truncate(tr.TokenType, 32))
	}
	if err := ValidateCredential(tr.AccessToken); err != nil {
		return "", 0, errors.New("oauth: token endpoint returned no usable access_token")
	}

	lifetime := defaultTokenLifetime
	if tr.ExpiresIn != "" {
		if secs, err := tr.ExpiresIn.Int64(); err == nil && secs > 0 {
			lifetime = time.Duration(secs) * time.Second
		}
	}
	return tr.AccessToken, lifetime, nil
}

// isSafeErrorCode accepts only the shape of a registered OAuth error code
// (RFC 6749 §5.2: invalid_client, invalid_scope, ...), so an unexpected body
// can't smuggle arbitrary text into an error message.
func isSafeErrorCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type cachedToken struct {
	token       string
	expires     time.Time
	fingerprint [sha256.Size]byte
}

// OAuthTokenCache keeps client_credentials access tokens in memory until they
// are about to expire, so each MCP call doesn't pay a token round-trip -- and
// doesn't hammer the authorization server, many of which rate-limit or bill by
// token request. Tokens are never persisted: a restart just fetches a new one.
type OAuthTokenCache struct {
	mu      sync.Mutex
	entries map[string]cachedToken
	now     func() time.Time
	fetch   func(context.Context, OAuthConfig) (string, time.Duration, error)
}

func NewOAuthTokenCache() *OAuthTokenCache {
	return &OAuthTokenCache{
		entries: make(map[string]cachedToken),
		now:     time.Now,
		fetch:   fetchClientCredentialsToken,
	}
}

func fingerprint(cfg OAuthConfig) [sha256.Size]byte {
	return sha256.Sum256([]byte(cfg.TokenURL + "\x00" + cfg.ClientID + "\x00" + cfg.ClientSecret))
}

// Token returns a valid access token for key (an MCP server's id), fetching a
// fresh one when none is cached, the cached one is about to expire, or the
// registration changed since it was fetched -- the fingerprint covers the
// token URL, client id and secret, so rotating a credential in Settings takes
// effect on the very next call rather than after the old token's expiry. Only
// a hash of the registration is retained, not the secret itself.
func (c *OAuthTokenCache) Token(ctx context.Context, key string, cfg OAuthConfig) (string, error) {
	fp := fingerprint(cfg)

	c.mu.Lock()
	if e, ok := c.entries[key]; ok &&
		subtle.ConstantTimeCompare(e.fingerprint[:], fp[:]) == 1 &&
		c.now().Before(e.expires) {
		token := e.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	// Fetched outside the lock: a slow token endpoint must not stall every
	// other server's calls. Two concurrent misses may both fetch, which is
	// harmless -- the last write wins and both tokens are valid.
	token, lifetime, err := c.fetch(ctx, cfg)
	if err != nil {
		return "", err
	}
	ttl := min(lifetime-tokenExpirySkew, maxTokenCacheTTL)

	c.mu.Lock()
	defer c.mu.Unlock()
	if ttl > 0 {
		c.entries[key] = cachedToken{token: token, expires: c.now().Add(ttl), fingerprint: fp}
	} else {
		delete(c.entries, key)
	}
	return token, nil
}

// Invalidate drops the cached token for key, so the next call fetches a fresh
// one. Called when a server rejects a request, since the likeliest cause of a
// sudden 401 on a cached token is that it was revoked or the auth server
// rotated its keys.
func (c *OAuthTokenCache) Invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}
