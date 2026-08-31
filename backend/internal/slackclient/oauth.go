// Package slackclient is a thin wrapper around the Slack Web API calls
// KuruOps's Slack integration needs -- mirrors internal/mcpclient's role
// as a small, dependency-free HTTP client for one external API, rather than
// pulling in a full third-party Slack SDK for what's currently a single
// endpoint.
//
// Today this only covers the OAuth token exchange (see ExchangeCode) --
// the foundation-only phase of the integration (connect/disconnect a
// workspace, nothing else). Future phases add the bot-token-authenticated
// calls here as they're built: conversations.create/conversations.replies
// (pull a channel's/thread's messages), chat.postMessage (post from
// KuruOps into Slack), files.info (fetch an attachment a thread
// referenced). None of those exist yet -- there is no bot-token-authenticated
// client type in this package until a feature actually needs one.
package slackclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oauthAccessURL is Slack's OAuth v2 token exchange endpoint -- not a
// per-tenant setting, so there's no config plumbing for it; a var (not a
// const) purely so oauth_internal_test.go can redirect it at a local test
// server, same convention as llmclient's anthropicAPIURL.
var oauthAccessURL = "https://slack.com/api/oauth.v2.access"

// OAuthResult is the subset of Slack's oauth.v2.access response KuruOps
// actually stores -- see SlackConfigService.HandleOAuthCallback.
type OAuthResult struct {
	AccessToken string // the bot token (xoxb-...), stored via secrets.Store
	TeamID      string
	TeamName    string
	BotUserID   string
	Scope       string // comma-separated, as Slack returns it -- display only
}

// oauthHTTPClient bounds the token exchange, which http.DefaultClient would
// not: /auth/oauth/* is mounted outside the api group's chimw.Timeout (see
// router.go), so the request context carries no deadline either -- a
// blackholed connection to slack.com would otherwise pin this goroutine and
// its connection indefinitely. No httpguard here (unlike webhook/LLM/MCP
// destinations): oauthAccessURL is a package constant, never tenant input.
var oauthHTTPClient = &http.Client{Timeout: 15 * time.Second}

// ExchangeCode performs Slack's OAuth v2 token exchange -- the bot-scoped
// counterpart of golang.org/x/oauth2's Config.Exchange, hand-rolled because
// Slack's response shape (nested "team"/"authed_user" objects, an "ok"
// boolean instead of an HTTP error status for failures) doesn't fit
// oauth2.Config's generic token response parsing.
func ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURL string) (*OAuthResult, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURL},
	}
	// client_secret goes in the POST body, not the URL query -- unlike the
	// authorize-URL redirect (which is inherently a visible browser
	// navigation), this is a server-to-server call, and a query string can
	// end up in access logs / proxy logs / URL history in a way a request
	// body doesn't.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthAccessURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build oauth.v2.access request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call oauth.v2.access: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		BotUserID   string `json:"bot_user_id"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode oauth.v2.access response: %w", err)
	}
	if !body.OK {
		return nil, fmt.Errorf("slack oauth.v2.access failed: %s", body.Error)
	}
	if body.AccessToken == "" {
		return nil, fmt.Errorf("slack oauth.v2.access returned no bot token")
	}

	return &OAuthResult{
		AccessToken: body.AccessToken,
		TeamID:      body.Team.ID,
		TeamName:    body.Team.Name,
		BotUserID:   body.BotUserID,
		Scope:       body.Scope,
	}, nil
}
