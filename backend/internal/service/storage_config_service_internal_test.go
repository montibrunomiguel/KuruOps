package service

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rewriteTransport redirects every request to base's host, keeping the
// original path/query -- lets fetchGoogleAccountEmail's hardcoded
// googleapis.com URL be exercised against a local test server without
// changing the function itself (it's never meant to take a configurable
// endpoint outside tests, unlike slackclient's oauthAccessURL var, since
// nothing about this URL is a real per-deployment setting).
type rewriteTransport struct{ base *url.URL }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.base.Scheme
	req.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(req)
}

// TestFetchGoogleAccountEmail lives in-package (not service_test) so it can
// call the unexported fetchGoogleAccountEmail directly.
func TestFetchGoogleAccountEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth2/v2/userinfo", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"admin@example.com"}`))
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: rewriteTransport{base: base}}

	email, err := fetchGoogleAccountEmail(t.Context(), client)
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", email)
}

func TestFetchGoogleAccountEmail_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid token"))
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: rewriteTransport{base: base}}

	_, err = fetchGoogleAccountEmail(t.Context(), client)
	assert.ErrorContains(t, err, "401")
}
