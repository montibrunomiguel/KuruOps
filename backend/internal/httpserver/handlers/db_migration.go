package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/dbmigrate"
)

// DatabaseMigrationHandlers is Settings -> External Database: admin-only,
// same gate as every other /settings/... route. Unlike the rest of
// Settings, this doesn't read/write any of ArgusOps' own tables -- it
// drives dbmigrate.Service directly against a customer-supplied target,
// using sourcePool (this deployment's own bundled database) as the copy
// source. See dbmigrate.Service.Migrate for the full sequence and why this
// is a one-time, manual-restart cutover rather than a live hot-swap.
type DatabaseMigrationHandlers struct {
	svc        *dbmigrate.Service
	sourcePool *db.Pool
}

func NewDatabaseMigrationHandlers(svc *dbmigrate.Service, sourcePool *db.Pool) *DatabaseMigrationHandlers {
	return &DatabaseMigrationHandlers{svc: svc, sourcePool: sourcePool}
}

func (h *DatabaseMigrationHandlers) Routes(r chi.Router) {
	r.Post("/test-connection", h.testConnection)
	r.Post("/migrate", h.migrate)
}

type targetConfigRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslMode"`
}

func (req targetConfigRequest) toTarget() dbmigrate.TargetConfig {
	return dbmigrate.TargetConfig{
		Host: req.Host, Port: req.Port, Database: req.Database,
		User: req.User, Password: req.Password, SSLMode: req.SSLMode,
	}
}

func (h *DatabaseMigrationHandlers) testConnection(w http.ResponseWriter, r *http.Request) {
	var req targetConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.TestConnection(r.Context(), req.toTarget()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// migrate runs the full test -> schema -> roles -> copy sequence
// synchronously and returns the result in one response -- this can take
// anywhere from seconds to minutes depending on data volume, which is why
// the frontend shows a blocking "Migrating..." state rather than treating
// this like a quick settings save (see DatabaseMigrationPanel.tsx).
func (h *DatabaseMigrationHandlers) migrate(w http.ResponseWriter, r *http.Request) {
	var req targetConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.svc.Migrate(r.Context(), h.sourcePool, req.toTarget())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
