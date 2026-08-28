package slackclient

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExchangeCode lives in-package (not slackclient_test) so it can
// redirect oauthAccessURL at a local server -- the client always dials the
// real Slack API otherwise, same convention as llmclient's
// anthropic_internal_test.go.
func TestExchangeCode(t *testing.T) {
	var gotForm map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = map[string][]string(r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"access_token": "xoxb-test-token",
			"scope": "chat:write,channels:read",
			"bot_user_id": "U123",
			"team": {"id": "T456", "name": "Acme Corp"}
		}`))
	}))
	defer srv.Close()

	original := oauthAccessURL
	oauthAccessURL = srv.URL
	t.Cleanup(func() { oauthAccessURL = original })

	result, err := ExchangeCode(t.Context(), "client-id", "client-secret", "auth-code", "https://kuruops.example/auth/oauth/slack/callback")
	require.NoError(t, err)
	assert.Equal(t, "xoxb-test-token", result.AccessToken)
	assert.Equal(t, "T456", result.TeamID)
	assert.Equal(t, "Acme Corp", result.TeamName)
	assert.Equal(t, "U123", result.BotUserID)
	assert.Equal(t, "chat:write,channels:read", result.Scope)

	assert.Equal(t, []string{"client-id"}, gotForm["client_id"])
	assert.Equal(t, []string{"client-secret"}, gotForm["client_secret"])
	assert.Equal(t, []string{"auth-code"}, gotForm["code"])
	assert.Equal(t, []string{"https://kuruops.example/auth/oauth/slack/callback"}, gotForm["redirect_uri"])
}

func TestExchangeCode_SlackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": false, "error": "invalid_code"}`))
	}))
	defer srv.Close()

	original := oauthAccessURL
	oauthAccessURL = srv.URL
	t.Cleanup(func() { oauthAccessURL = original })

	_, err := ExchangeCode(t.Context(), "client-id", "client-secret", "bad-code", "https://kuruops.example/auth/oauth/slack/callback")
	assert.ErrorContains(t, err, "invalid_code")
}
