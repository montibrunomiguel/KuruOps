package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
)

// slowHandler answers after pause, the way the external-database migration
// answers after its copy finishes.
func slowHandler(pause time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(pause)
		_, _ = io.WriteString(w, "done")
	})
}

// get returns the body, or "" when the server cut the connection.
func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func serverWithWriteTimeout(t *testing.T, h http.Handler, d time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = d
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestExtendWriteDeadline(t *testing.T) {
	t.Run("a handler slower than the server's WriteTimeout is cut off without it", func(t *testing.T) {
		// The control: proves the server really enforces WriteTimeout, so the
		// next case is not passing vacuously.
		srv := serverWithWriteTimeout(t, slowHandler(600*time.Millisecond), 200*time.Millisecond)
		assert.Empty(t, get(t, srv.URL))
	})

	t.Run("the same handler completes when the route extends its deadline", func(t *testing.T) {
		srv := serverWithWriteTimeout(t,
			middleware.ExtendWriteDeadline(3*time.Second)(slowHandler(600*time.Millisecond)),
			200*time.Millisecond)
		assert.Equal(t, "done", get(t, srv.URL))
	})

	t.Run("the extension is bounded -- it does not remove the deadline", func(t *testing.T) {
		// A hung migration must still release its connection eventually.
		srv := serverWithWriteTimeout(t,
			middleware.ExtendWriteDeadline(300*time.Millisecond)(slowHandler(900*time.Millisecond)),
			10*time.Second)
		assert.Empty(t, get(t, srv.URL))
	})

	t.Run("a writer that cannot take a deadline is left alone", func(t *testing.T) {
		// httptest.ResponseRecorder does not implement it; the middleware must
		// fall back to the server default silently rather than panic or fail.
		rec := httptest.NewRecorder()
		called := false
		middleware.ExtendWriteDeadline(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		require.True(t, called)
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}
