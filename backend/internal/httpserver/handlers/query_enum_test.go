package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
)

// A value outside an enum filter is the caller's mistake: it must be a 400 that
// names the bad value, not a 500 from Postgres rejecting the enum cast.
func TestAlertHandlers_List_InvalidEnumFilterIs400(t *testing.T) {
	h, tenantID, actorID, _ := newAlertHandlerFixture(t)
	r := newRouter(h.Routes)

	get := func(query string) *httptest.ResponseRecorder {
		return doRequest(r, withClaims(httptest.NewRequest("GET", "/?"+query, nil), tenantID, actorID, nil))
	}

	for name, query := range map[string]string{
		"unknown severity":                 "severity=bogus",
		"valid then unknown severity":      "severity=high,bogus",
		"unknown status":                   "status=resolved",
		"unknown among several statuses":   "status=open,investigating,nope",
		"a phase passed as a severity":     "severity=containment",
		"case matters (enums are lowered)": "severity=HIGH",
	} {
		t.Run(name+" -- 400", func(t *testing.T) {
			rec := get(query)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "invalid")
		})
	}

	t.Run("the error names the offending value but truncates a huge one", func(t *testing.T) {
		rec := get("severity=" + strings.Repeat("x", 500))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Less(t, rec.Body.Len(), 200)
	})

	t.Run("valid multi-value filters still work, and blanks from stray commas are ignored", func(t *testing.T) {
		rec := get("severity=high,&status=open,")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var alerts []domain.Alert
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &alerts))
		assert.Len(t, alerts, 1)
	})
}

func TestIncidentHandlers_List_InvalidEnumFilterIs400(t *testing.T) {
	h, tenantID, actorID, _ := newIncidentHandlerFixture(t)
	r := newRouter(h.Routes)

	get := func(query string) *httptest.ResponseRecorder {
		return doRequest(r, withClaims(httptest.NewRequest("GET", "/?"+query, nil), tenantID, actorID, nil))
	}

	for name, query := range map[string]string{
		"unknown severity":            "severity=bogus",
		"valid then unknown severity": "severity=critical,bogus",
		"unknown phase":               "phase=archived",
		"unknown among several":       "phase=new,containment,nope",
		"unknown priority":            "priority=p9",
	} {
		t.Run(name+" -- 400", func(t *testing.T) {
			rec := get(query)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "invalid")
		})
	}

	t.Run("valid filters still work", func(t *testing.T) {
		rec := get("severity=critical,medium&phase=new,containment&priority=p1")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var incidents []domain.Incident
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &incidents))
		assert.Len(t, incidents, 1)
	})
}
