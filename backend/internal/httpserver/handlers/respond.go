package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
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
