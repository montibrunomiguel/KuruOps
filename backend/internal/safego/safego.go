// Package safego wraps launching a detached goroutine so a panic inside it
// can never crash the whole process. chi.Recoverer (see
// internal/httpserver/middleware) only protects the synchronous HTTP
// request path -- it has no way to catch a panic in a goroutine spawned
// with a bare `go func() {...}()`, since that panic unwinds on its own
// goroutine's stack and, left unrecovered, takes the entire binary down
// with it (an unrecovered panic on any goroutine terminates the process,
// not just that goroutine). This codebase spawns several such goroutines
// for background work that must outlive the request that triggered it --
// AI analysis runs, manual-escalation notification sends, an external-DB
// migration's row-copy progress loop -- none of which run under
// chi.Recoverer's protection. Go is the one way any of them should be
// launched.
package safego

import (
	"log/slog"
	"runtime/debug"
)

// Go runs fn on a new goroutine, recovering any panic and logging it
// (message, recovered value, and a stack trace) instead of letting it
// crash the process. name identifies the call site in the log line --
// pick something that would let whoever reads the log find the right
// goroutine without also needing the stack trace (e.g.
// "ai-analysis.finishSimpleRun", "alert.autoAnalyze").
func Go(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in background goroutine", "goroutine", name, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}
