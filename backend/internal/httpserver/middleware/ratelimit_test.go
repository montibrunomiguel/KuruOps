package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/httpserver/middleware"
)

func TestKeyedLimiter_Allow(t *testing.T) {
	limiter := middleware.NewKeyedLimiter(2, time.Minute)

	assert.True(t, limiter.Allow("a@test.local"), "1st request for this key is allowed")
	assert.True(t, limiter.Allow("a@test.local"), "2nd request for this key is allowed")
	assert.False(t, limiter.Allow("a@test.local"), "3rd request exceeds the limit")

	t.Run("a different key has its own independent budget", func(t *testing.T) {
		assert.True(t, limiter.Allow("b@test.local"))
		assert.True(t, limiter.Allow("b@test.local"))
		assert.False(t, limiter.Allow("b@test.local"))
	})
}

func TestNewRateLimiter_PerIP(t *testing.T) {
	limiter := middleware.NewRateLimiter(2, time.Minute)
	handler := limiter(okHandler())

	newReq := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	assert.Equal(t, http.StatusTooManyRequests, newReq("10.0.0.1").Code, "3rd request from the same IP is rejected")

	assert.Equal(t, http.StatusOK, newReq("10.0.0.2").Code, "a different IP has its own independent budget")
}

func TestNewRateLimiter_IgnoresSpoofedXForwardedFor(t *testing.T) {
	limiter := middleware.NewRateLimiter(2, time.Minute)
	handler := limiter(okHandler())

	// Same RemoteAddr (as if a single client connected to nginx once), but a
	// different X-Forwarded-For on every request -- if XFF were trusted (the
	// bug this test guards against), each request would get its own budget
	// and the limiter would never trigger.
	newReq := func(spoofedXFF string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", spoofedXFF)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, newReq("1.1.1.1").Code)
	require.Equal(t, http.StatusOK, newReq("2.2.2.2").Code)
	assert.Equal(t, http.StatusTooManyRequests, newReq("3.3.3.3").Code, "still rate limited despite a different spoofed X-Forwarded-For on every request")
}

func TestNewRateLimiter_TrustsXRealIPFromProxy(t *testing.T) {
	limiter := middleware.NewRateLimiter(2, time.Minute)
	handler := limiter(okHandler())

	// nginx.conf sets X-Real-IP unconditionally, so it's the trusted signal
	// -- two different X-Real-IP values (as nginx would set for two distinct
	// real clients) get independent budgets even from the same RemoteAddr
	// (nginx's own connection to the backend).
	newReq := func(realIP string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		req.Header.Set("X-Real-IP", realIP)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	assert.Equal(t, http.StatusTooManyRequests, newReq("10.0.0.1").Code, "3rd request from the same X-Real-IP is rejected")

	assert.Equal(t, http.StatusOK, newReq("10.0.0.2").Code, "a different X-Real-IP has its own independent budget")
}
