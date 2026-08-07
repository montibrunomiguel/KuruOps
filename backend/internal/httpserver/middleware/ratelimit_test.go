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
