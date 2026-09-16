// Package sessioncookie owns how a refresh token is carried to and from a
// browser: the cookie's name, its attributes, and how long it lives.
//
// It is its own package because two layers need it and neither should
// import the other -- the /auth handlers, and service.SAMLAuthService,
// which writes its own HTTP response at the end of the IdP round-trip.
// Putting the lifetime here too keeps the one invariant that matters in a
// single file: the cookie's MaxAge and the token's own expiry are the same
// number, so a session cannot outlive its cookie or vice versa.
package sessioncookie

import (
	"net/http"
	"strings"
	"time"
)

// Name is the cookie the refresh token lives in.
const Name = "kuruops_refresh"

// RefreshTTL is how long a refresh token stays valid after issuance or
// rotation -- deliberately much longer than the 15-minute access token
// (authn.Issuer), since re-authenticating every 15 minutes would make the
// short access-token TTL pointless from a usability standpoint. The
// tradeoff is bounded by AuthService.RevokeSessions, not by a short TTL
// here.
const RefreshTTL = 30 * 24 * time.Hour

// refreshCookiePath scopes the cookie to the only routes that ever need it
// -- /auth/refresh and /auth/logout. Every authenticated request under
// /api/v1 carries the short-lived access token in an Authorization header
// instead, so there is no reason to attach a 30-day credential to all of
// them: a narrower Path means fewer requests that can leak it in a proxy
// log or an error report.
const path = "/auth"

// Secure decides whether to set the Secure attribute, from the
// scheme of the externally-reachable frontend origin the operator already
// had to configure (config.Config.AppBaseURL).
//
// Deriving it beats a dedicated env var in both failure directions. A
// hardcoded Secure would silently break every self-hosted deployment on
// plain HTTP -- the browser drops the cookie, login appears to work, and
// the user is thrown out the moment the access token expires, with nothing
// in any log to explain it. A hardcoded non-Secure would ship the refresh
// token over cleartext on real HTTPS deployments. APP_BASE_URL already
// states which world the deployment lives in.
//
// Note that http://localhost is the one cleartext origin browsers still
// treat as trustworthy, so a laptop dev stack works either way.
func Secure(appBaseURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(appBaseURL)), "https://")
}

// Set stores the refresh token as an HttpOnly cookie.
//
// HttpOnly is the point of the whole exercise: script on the page cannot
// read the value, so an XSS foothold can no longer exfiltrate a 30-day
// credential to an attacker-controlled host and keep minting access tokens
// long after the page is closed. It does not make XSS harmless -- script on
// the page can still call /auth/refresh and use the access token it gets
// back -- but it confines the damage to the lifetime of the injected
// script instead of handing over a portable, month-long session.
//
// SameSite=Strict is safe here because nothing cross-site is supposed to
// reach /auth/refresh or /auth/logout: the SPA calls both from its own
// origin. It also means a cross-site request cannot trigger a token
// rotation or a logout on the user's behalf.
func Set(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     Name,
		Value:    token,
		Path:     path,
		MaxAge:   int(RefreshTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// Clear expires the cookie. Every attribute except Value and
// MaxAge has to match what Set wrote -- a browser keys a
// cookie on name+domain+path, so clearing it from a different Path leaves
// the original in place and the session silently survives a logout.
func Clear(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     Name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// Read returns the refresh token presented by the caller, or ""
// when there is none.
//
// Cookie only, on purpose: accepting it from the request body as well would
// undo the migration. The reason the token is no longer in any response
// body is so that script on the page has no way to obtain one -- leaving a
// body path open would just mean an attacker who can run script can also
// replay whatever they scraped, and every non-browser client would keep
// using the weaker path indefinitely.
func Read(r *http.Request) string {
	c, err := r.Cookie(Name)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}
