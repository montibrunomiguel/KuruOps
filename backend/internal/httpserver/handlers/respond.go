package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// mustTenantID reads the tenant ID out of the request context, writing a 401
// and returning ok=false if it's missing -- every handler entry point should
// call this instead of middleware.TenantID directly, so a missing tenant
// context is always rejected rather than silently proceeding with a zero
// uuid.UUID.
func mustTenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return uuid.UUID{}, false
	}
	return tenantID, true
}

// decodeAndParseID parses the "id" URL param and decodes the request body
// into req, writing the matching 400 and returning ok=false on either
// failure -- entity names the resource in the parse-error message (e.g.
// "alert" -> "invalid alert id"), matching what each handler already said
// before this was factored out.
func decodeAndParseID[T any](w http.ResponseWriter, r *http.Request, entity string, req *T) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+entity+" id")
		return uuid.UUID{}, false
	}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return uuid.UUID{}, false
	}
	return id, true
}

// maxPageLimit mirrors the cap already enforced independently in
// repository.ListAlertsFilter/ListIncidentsFilter (limit <= 0 or > 200 falls
// back to 50 there too) -- kept here as well so an out-of-range value is
// normalized before it ever reaches the repository layer.
const maxPageLimit = 200

// parsePaging reads limit/offset query params for a list endpoint. Both are
// optional; an invalid or out-of-range limit falls back to 0 (the
// repository's own default of 50 applies), never to maxPageLimit, so a
// bogus limit can't silently request more rows than the caller asked for.
func parsePaging(r *http.Request) (limit, offset int) {
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= maxPageLimit {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	return limit, offset
}
