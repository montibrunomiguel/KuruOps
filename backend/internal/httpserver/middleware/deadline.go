package middleware

import (
	"net/http"
	"time"
)

// ExtendWriteDeadline moves the connection's write deadline to now+d for the
// requests it wraps, overriding http.Server.WriteTimeout for them alone.
//
// The server-wide WriteTimeout exists so a client that never reads its response
// cannot hold a connection forever. The few routes that legitimately outlive it
// (the external-database migration, which runs for minutes) opt out per request
// instead of the whole server being given their generous limit -- the same
// shape as their per-route chimw.Timeout.
//
// It uses http.ResponseController, which reaches the underlying connection
// through any wrapper that implements Unwrap (chi's do). Where that is not
// possible -- a test ResponseRecorder, an HTTP/2 stream that rejects it -- the
// extension is skipped and the server default applies, which fails safe.
func ExtendWriteDeadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
			next.ServeHTTP(w, r)
		})
	}
}
