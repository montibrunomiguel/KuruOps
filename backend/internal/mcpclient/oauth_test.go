package mcpclient_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/mcpclient"
)

// tokenServer is a stub authorization server. respond decides the reply to
// each token request; the request is captured for assertions.
type tokenServer struct {
	*httptest.Server
	calls   atomic.Int32
	lastReq *http.Request
	lastErr error
	form    url.Values
}

func newTokenServer(t *testing.T, respond func(w http.ResponseWriter, call int32)) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := ts.calls.Add(1)
		ts.lastReq = r
		ts.lastErr = r.ParseForm()
		ts.form = r.PostForm
		respond(w, n)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func jsonReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func cfgFor(ts *tokenServer) mcpclient.OAuthConfig {
	return mcpclient.OAuthConfig{TokenURL: ts.URL, ClientID: "my-client", ClientSecret: "s3cr3t"}
}

func TestOAuthTokenCache_FetchesWithClientCredentialsAndBasicAuth(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		jsonReply(w, 200, `{"access_token":"tok-1","token_type":"Bearer","expires_in":3600}`)
	})

	token, err := mcpclient.NewOAuthTokenCache().Token(t.Context(), "srv", cfgFor(ts))

	require.NoError(t, err)
	assert.Equal(t, "tok-1", token)
	require.NoError(t, ts.lastErr)
	assert.Equal(t, http.MethodPost, ts.lastReq.Method)
	assert.Equal(t, "client_credentials", ts.form.Get("grant_type"))
	assert.Empty(t, ts.form.Get("client_secret"), "the secret must not travel in the body")
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("my-client:s3cr3t")), ts.lastReq.Header.Get("Authorization"))
}

func TestOAuthTokenCache_FormEncodesSpecialCharactersInCredentials(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		jsonReply(w, 200, `{"access_token":"tok","expires_in":3600}`)
	})
	cfg := mcpclient.OAuthConfig{TokenURL: ts.URL, ClientID: "id:with:colons", ClientSecret: "p%ss word+&"}

	_, err := mcpclient.NewOAuthTokenCache().Token(t.Context(), "srv", cfg)
	require.NoError(t, err)

	user, pass, ok := ts.lastReq.BasicAuth()
	require.True(t, ok)
	// RFC 6749 §2.3.1: form-urlencoded before base64, so the server can
	// split on the first ':' and url-decode each half.
	assert.Equal(t, url.QueryEscape("id:with:colons"), user)
	assert.Equal(t, url.QueryEscape("p%ss word+&"), pass)
}

func TestOAuthTokenCache_CachesUntilExpiry(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, call int32) {
		jsonReply(w, 200, `{"access_token":"tok","expires_in":3600}`)
	})
	cache := mcpclient.NewOAuthTokenCache()

	for range 3 {
		_, err := cache.Token(t.Context(), "srv", cfgFor(ts))
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), ts.calls.Load(), "a valid token must be reused")
}

func TestOAuthTokenCache_ShortLivedTokenIsNotCached(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		// Inside the expiry-skew window: it would be considered expired
		// immediately, so caching it can only hand out a stale token.
		jsonReply(w, 200, `{"access_token":"tok","expires_in":10}`)
	})
	cache := mcpclient.NewOAuthTokenCache()

	_, err := cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)
	_, err = cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)

	assert.Equal(t, int32(2), ts.calls.Load())
}

func TestOAuthTokenCache_RotatedRegistrationBypassesCache(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, call int32) {
		jsonReply(w, 200, `{"access_token":"tok","expires_in":3600}`)
	})
	cache := mcpclient.NewOAuthTokenCache()
	cfg := cfgFor(ts)

	_, err := cache.Token(t.Context(), "srv", cfg)
	require.NoError(t, err)
	cfg.ClientSecret = "rotated"
	_, err = cache.Token(t.Context(), "srv", cfg)
	require.NoError(t, err)

	assert.Equal(t, int32(2), ts.calls.Load(), "a changed secret must not be served a token minted with the old one")
}

func TestOAuthTokenCache_InvalidateForcesRefetch(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		jsonReply(w, 200, `{"access_token":"tok","expires_in":3600}`)
	})
	cache := mcpclient.NewOAuthTokenCache()

	_, err := cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)
	cache.Invalidate("srv")
	_, err = cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)

	assert.Equal(t, int32(2), ts.calls.Load())
}

func TestOAuthTokenCache_KeysAreIsolated(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, call int32) {
		jsonReply(w, 200, `{"access_token":"tok","expires_in":3600}`)
	})
	cache := mcpclient.NewOAuthTokenCache()

	_, err := cache.Token(t.Context(), "server-a", cfgFor(ts))
	require.NoError(t, err)
	_, err = cache.Token(t.Context(), "server-b", cfgFor(ts))
	require.NoError(t, err)

	assert.Equal(t, int32(2), ts.calls.Load())
}

func TestOAuthTokenCache_Errors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"invalid_client surfaces the standard code": {401, `{"error":"invalid_client","error_description":"bad secret for client my-client"}`, "invalid_client"},
		"unexpected body is not echoed":             {500, `<html>internal: s3cr3t leaked</html>`, "returned 500"},
		"non-json success":                          {200, `not json`, "did not return JSON"},
		"missing access token":                      {200, `{"token_type":"Bearer"}`, "no usable access_token"},
		"unsupported token type":                    {200, `{"access_token":"x","token_type":"mac"}`, "unsupported token_type"},
		"control char in token":                     {200, `{"access_token":"a\nb"}`, "no usable access_token"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) { jsonReply(w, tc.status, tc.body) })

			_, err := mcpclient.NewOAuthTokenCache().Token(t.Context(), "srv", cfgFor(ts))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "s3cr3t", "an error must never carry the secret or the response body")
			assert.NotContains(t, err.Error(), "bad secret for client", "error_description is attacker-controlled text and must not be echoed")
		})
	}
}

func TestOAuthTokenCache_DoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		jsonReply(w, 200, `{"access_token":"stolen","expires_in":3600}`)
	}))
	defer target.Close()
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	})

	_, err := mcpclient.NewOAuthTokenCache().Token(t.Context(), "srv", cfgFor(ts))

	require.Error(t, err)
	assert.False(t, reached.Load(), "client credentials must not be replayed to a redirect target")
}

func TestOAuthTokenCache_MissingExpiresInDefaultsToShortLifetime(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		jsonReply(w, 200, `{"access_token":"tok"}`)
	})
	cache := mcpclient.NewOAuthTokenCache()

	_, err := cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)
	_, err = cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)

	assert.Equal(t, int32(1), ts.calls.Load(), "the default 5-minute lifetime is cached")
}

func TestOAuthTokenCache_AcceptsExpiresInAsString(t *testing.T) {
	ts := newTokenServer(t, func(w http.ResponseWriter, _ int32) {
		// Some authorization servers quote the number.
		jsonReply(w, 200, `{"access_token":"tok","expires_in":"3600"}`)
	})
	cache := mcpclient.NewOAuthTokenCache()

	_, err := cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)
	_, err = cache.Token(t.Context(), "srv", cfgFor(ts))
	require.NoError(t, err)

	assert.Equal(t, int32(1), ts.calls.Load())
}
