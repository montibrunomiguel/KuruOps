package handlers

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// UploadHandlers backs the one piece of file-storage infrastructure in
// ArgusOps: evidence images attached to alert close-comments and
// alert/incident Team Notes. Where the bytes actually land is decided by
// service.StorageConfigService.BuildStore per request -- a tenant's
// configured S3/GCS bucket if one exists (see Settings -> Storage
// Integration), local disk (UPLOAD_DIR) otherwise. Every object key follows
// the same layout regardless of backend: <Alert|Incident>/<yyyy>/<mm>/<dd>/
// <id>_<slugified title>/<uuid>.<ext> -- grouping evidence by the alert or
// incident it belongs to and by the day it was captured, browsable directly
// in a bucket console without going through the app.
type UploadHandlers struct {
	storageConfig *service.StorageConfigService
	alerts        *service.AlertService
	incidents     *service.IncidentService
	maxBytes      int64
}

// defaultMaxUploadBytes caps a single attached image at 5MB -- generous for
// a screenshot, small enough that a careless client can't fill the disk (or
// run up a cloud storage bill) in one request.
const defaultMaxUploadBytes = 5 << 20

// allowedImageTypes maps a sniffed Content-Type (via http.DetectContentType
// on the actual bytes, never the client-supplied header) to the extension
// the file is stored under.
var allowedImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// keyRE validates the wildcard portion of a GET before it's handed to any
// blobstore.Store implementation -- for LocalStore this is what blocks path
// traversal (no "..", no leading "/", every segment matches a known shape);
// S3/GCS have no traversal risk of their own, but validating unconditionally
// keeps one rule for all three backends instead of a local-disk special case.
var keyRE = regexp.MustCompile(`^(Alert|Incident)/\d{4}/\d{2}/\d{2}/[0-9a-f-]{36}_[A-Za-z0-9._-]{1,80}/[0-9a-f-]{36}\.[a-z0-9]{3,4}$`)

// slugRE keeps a title's path segment to characters that are safe (and
// readable) across local filesystems, S3 keys, and GCS object names alike.
var slugRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func NewUploadHandlers(storageConfig *service.StorageConfigService, alerts *service.AlertService, incidents *service.IncidentService) *UploadHandlers {
	return &UploadHandlers{storageConfig: storageConfig, alerts: alerts, incidents: incidents, maxBytes: defaultMaxUploadBytes}
}

// Routes is mounted at /api/v1/uploads/images -- POST / uploads a new file
// attached to a given alert/incident, GET /* serves one back by its full
// key (the wildcard captures every "/"-separated segment after /images/).
func (h *UploadHandlers) Routes(r chi.Router) {
	r.Post("/", h.upload)
	r.Get("/*", h.serve)
}

func (h *UploadHandlers) upload(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	allowedTags := middleware.AllowedTags(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes)
	if err := r.ParseMultipartForm(h.maxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or malformed upload")
		return
	}

	kind := r.FormValue("kind")
	id, err := uuid.Parse(r.FormValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or invalid id")
		return
	}

	prefix, title, err := h.resolveEntity(r, tenantID, kind, id, allowedTags)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	// Sniff the first 512 bytes to determine the real type -- a client
	// could lie in the multipart Content-Type header, this can't. multipart
	// files are always seekable (backed by memory or a temp file), so
	// rewind afterward rather than stitching the head back onto the stream.
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		writeError(w, http.StatusBadRequest, "could not read uploaded file")
		return
	}
	head = head[:n]
	contentType := http.DetectContentType(head)

	ext, ok := allowedImageTypes[contentType]
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported image type: only PNG, JPEG, GIF, and WebP are allowed")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "could not read uploaded file")
		return
	}

	now := time.Now().UTC()
	key := fmt.Sprintf("%s/%04d/%02d/%02d/%s_%s/%s%s",
		prefix, now.Year(), now.Month(), now.Day(), id, slugify(title), uuid.New(), ext,
	)

	store, err := h.storageConfig.BuildStore(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reach configured storage backend")
		return
	}

	if err := store.Put(r.Context(), key, file, fileHeader.Size, contentType); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store uploaded file")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"url": "/api/v1/uploads/images/" + key})
}

// resolveEntity loads the alert/incident the upload is attached to (so its
// title can go into the object key) and enforces the same tag-visibility
// rule every other alert/incident endpoint does -- an analyst without
// access to a tag-restricted alert can't attach evidence to it either.
// Returns the path prefix ("Alert"/"Incident") and the entity's title.
func (h *UploadHandlers) resolveEntity(r *http.Request, tenantID uuid.UUID, kind string, id uuid.UUID, allowedTags []string) (prefix, title string, err error) {
	switch kind {
	case "alert":
		a, err := h.alerts.Get(r.Context(), tenantID, id, allowedTags)
		if err != nil {
			return "", "", err
		}
		if a == nil {
			return "", "", fmt.Errorf("alert %s not found", id)
		}
		return "Alert", a.Title, nil
	case "incident":
		inc, err := h.incidents.Get(r.Context(), tenantID, id, allowedTags)
		if err != nil {
			return "", "", err
		}
		if inc == nil {
			return "", "", fmt.Errorf("incident %s not found", id)
		}
		return "Incident", inc.Title, nil
	default:
		return "", "", fmt.Errorf(`kind must be "alert" or "incident"`)
	}
}

func slugify(title string) string {
	slug := slugRE.ReplaceAllString(strings.TrimSpace(title), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 80 {
		slug = strings.Trim(slug[:80], "-")
	}
	if slug == "" {
		slug = "untitled"
	}
	return slug
}

func (h *UploadHandlers) serve(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	key := chi.URLParam(r, "*")
	if !keyRE.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid key")
		return
	}

	store, err := h.storageConfig.BuildStore(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reach configured storage backend")
		return
	}

	rc, contentType, err := store.Get(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", contentType)
	_, _ = io.Copy(w, rc)
}
