package middleware

import "net/http"

// SecurityHeaders applies standard security headers to every response --
// API and /healthz/metrics alike, since this middleware sits above the
// entire router (see NewRouter). It only ever serves JSON or plain text,
// never HTML, so the CSP here can be maximally strict: no script, style,
// frame, or anything else is ever legitimate on an API response.
//
// The SPA itself is served by nginx, a separate process from this Go
// binary, and needs a much more permissive CSP to actually render (inline
// `style={{...}}` throughout the React tree, Google Fonts) -- see
// frontend/nginx.conf, which sets its own CSP scoped to the HTML-serving
// locations only, not the proxied /api and /auth locations, so the two
// policies never collide on the same response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// frame-ancestors backs up X-Frame-Options for browsers that only
		// honor the newer CSP directive; default-src 'none' is safe because
		// an API response never itself loads a script/style/image/frame.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		// Only takes effect over HTTPS -- browsers ignore it on the plain
		// HTTP local dev uses, so this is harmless to always set, and means
		// a production deployment terminating TLS at nginx doesn't need a
		// second place to configure it.
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// X-XSS-Protection deliberately NOT set: it's deprecated (Chrome
		// removed its XSS Auditor entirely in 2019 after the auditor itself
		// was found to introduce bugs) and current OWASP guidance is to omit
		// it rather than set any value, relying on CSP instead.
		next.ServeHTTP(w, r)
	})
}
