package handlers

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/service"
)

// OAuthCallbackHandlers serves every 3rd-party OAuth provider's callback
// (Google Drive today, Slack in a later PR) -- deliberately mounted
// unauthenticated at the top level (/auth/oauth/..., see router.go), NOT
// under /api/v1: every /api/v1 route requires a Bearer JWT
// (middleware.JWTAuth), but a provider's redirect is a plain top-level
// browser GET with no way to attach one. Resolves the single tenant every
// deployment has the same way AuthHandlers' SAML ACS endpoint already
// does (there is no session yet at this point, only whatever the
// oauth_states row -- keyed off the callback's own `state` param --
// records about who started the flow). Every outcome ends in a redirect
// back to the relevant Settings page, success or failure alike -- this is
// a top-level navigation, not an XHR call, so there's no caller to hand a
// JSON error response to.
type OAuthCallbackHandlers struct {
	auth          *service.AuthService
	storageConfig *service.StorageConfigService
	appBaseURL    string
}

func NewOAuthCallbackHandlers(auth *service.AuthService, storageConfig *service.StorageConfigService, appBaseURL string) *OAuthCallbackHandlers {
	return &OAuthCallbackHandlers{auth: auth, storageConfig: storageConfig, appBaseURL: appBaseURL}
}

func (h *OAuthCallbackHandlers) Routes(r chi.Router) {
	r.Get("/gdrive/callback", h.gdriveCallback)
}

func (h *OAuthCallbackHandlers) gdriveCallback(w http.ResponseWriter, r *http.Request) {
	settingsURL := h.appBaseURL + "/settings/storage"

	tenant, err := h.auth.ResolveDefaultTenant(r.Context())
	if err != nil || tenant == nil {
		http.Redirect(w, r, settingsURL+"?gdrive_error="+url.QueryEscape("internal error resolving tenant"), http.StatusFound)
		return
	}

	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		// The admin declined consent on Google's screen, or Google itself
		// rejected the request -- not a bug on this end.
		http.Redirect(w, r, settingsURL+"?gdrive_error="+url.QueryEscape(errParam), http.StatusFound)
		return
	}

	err = h.storageConfig.HandleGDriveOAuthCallback(r.Context(), tenant.ID, q.Get("code"), q.Get("state"))
	if err != nil {
		http.Redirect(w, r, settingsURL+"?gdrive_error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, settingsURL+"?gdrive_connected=1", http.StatusFound)
}
