package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/dbmigrate"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestDatabaseMigrationHandlers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	svc := dbmigrate.NewService("/nonexistent/migrations")
	h := handlers.NewDatabaseMigrationHandlers(svc, pool)
	r := newRouter(h.Routes)

	t.Run("test-connection with an invalid body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/test-connection", bytes.NewReader([]byte("not json")))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("test-connection against an unreachable host -- 400 with the underlying error", func(t *testing.T) {
		body := []byte(`{"host":"no-such-host.invalid","port":5432,"database":"x","user":"postgres","password":"x"}`)
		req := httptest.NewRequest("POST", "/test-connection", bytes.NewReader(body))
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "connect to target database")
	})

	t.Run("migrate with an invalid body -- 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/migrate", bytes.NewReader([]byte("not json")))
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("migrate against an unreachable host -- 400 before ever touching the source", func(t *testing.T) {
		body := []byte(`{"host":"no-such-host.invalid","port":5432,"database":"x","user":"postgres","password":"x"}`)
		req := httptest.NewRequest("POST", "/migrate", bytes.NewReader(body))
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
