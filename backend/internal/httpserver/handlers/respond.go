package handlers

import (
	"encoding/json"
	"log/slog"
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

// writeInternalError is writeError(w, http.StatusInternalServerError, ...)'s
// safe replacement for every handler that was writing err.Error() straight
// into a 500 response body. A raw Go error string routinely contains
// internal detail that has no business reaching an API client -- a
// Postgres constraint/column name, a driver-level error, an internal file
// path, sometimes literally a SQL fragment -- none of which helps a
// legitimate caller and all of which helps an attacker fingerprint the
// backend. The real error is still fully logged server-side (where every
// internal caller already looks for it), the client just gets a generic,
// safe message instead. Unlike writeError's other 4xx call sites (a
// validation message like "current password is incorrect" is deliberately
// client-safe and stays as-is), a 500 by definition means something on
// our side went wrong in a way the caller can't have caused or fixed, so
// there's nothing case-specific worth telling them anyway.
func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("internal server error", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "an internal error occurred")
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
// optional. A limit above maxPageLimit is clamped down to it rather than
// discarded: asking for 500 used to fall through to the repository's
// default of 50, so a caller who asked for MORE than the maximum got fewer
// rows than one who asked for exactly the maximum -- and nothing in the
// response said a cap had been applied. A non-numeric or non-positive
// limit still falls back to 0 (the repository default), since there is no
// sensible number to infer from garbage.
func parsePaging(r *http.Request) (limit, offset int) {
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		if v > maxPageLimit {
			v = maxPageLimit
		}
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	return limit, offset
}
