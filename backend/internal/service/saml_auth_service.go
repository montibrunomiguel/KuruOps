package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/sessioncookie"
)

// samlMetadataTTL bounds how long a cached IdP *saml.EntityDescriptor is
// trusted before ResolveIDPMetadata is called again -- balances staleness
// (an IdP rotating its signing cert) against the network-fetch-plus-XML-
// parse cost ServeLogin and ServeACS would otherwise both pay on every
// single login (see authn.ResolveIDPMetadata's doc comment).
const samlMetadataTTL = time.Hour

// samlCallbackPath is the SPA route ServeACS hands the browser to -- see
// frontend/src/pages/SamlCallback.tsx.
//
// Deliberately NOT under /auth/: frontend/nginx.conf proxies that whole
// prefix to the API, so a callback there would be answered by the backend
// router with a 404 instead of ever reaching the SPA. Caught by trying it
// in a browser, not by any test -- nothing in the Go or Vitest suites
// knows nginx exists.
const samlCallbackPath = "/login/saml"

type cachedSAMLMetadata struct {
	entityDescriptor *saml.EntityDescriptor
	fetchedAt        time.Time
}

// SAMLAuthService is the SAML half of "local + LDAP + SAML". It owns the
// three HTTP-facing steps of SP-initiated SSO (metadata, login redirect,
// ACS) and hands a verified assertion to AuthService for the same
// just-in-time provisioning + JWT issuance LDAP uses.
type SAMLAuthService struct {
	pool        *db.Pool
	identityCfg *repository.IdentityConfigRepository
	secrets     secrets.Store
	auth        *AuthService
	// secureCookies is the Secure attribute for the refresh cookie ServeACS
	// writes, derived once from APP_BASE_URL -- see sessioncookie.Secure.
	secureCookies bool
	// appBaseURL is where ServeACS sends the browser once the assertion has
	// been accepted -- the SPA's own origin, so the /auth/refresh call it
	// makes there is same-site.
	appBaseURL string

	metadataMu    sync.Mutex
	metadataCache map[uuid.UUID]cachedSAMLMetadata
}

func NewSAMLAuthService(pool *db.Pool, identityCfg *repository.IdentityConfigRepository, store secrets.Store, auth *AuthService, appBaseURL string) *SAMLAuthService {
	return &SAMLAuthService{
		pool: pool, identityCfg: identityCfg, secrets: store, auth: auth,
		secureCookies: sessioncookie.Secure(appBaseURL),
		appBaseURL:    appBaseURL,
		metadataCache: make(map[uuid.UUID]cachedSAMLMetadata),
	}
}

// InvalidateMetadataCache drops a tenant's cached IdP metadata so the next
// login re-resolves it immediately, instead of waiting out samlMetadataTTL
// -- wired to fire when IdentityConfigService.SaveSAMLConfig changes a
// tenant's IdP metadata source (see cmd/api/main.go).
func (s *SAMLAuthService) InvalidateMetadataCache(tenantID uuid.UUID) {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	delete(s.metadataCache, tenantID)
}

// resolveIDPMetadata returns the tenant's IdP metadata, serving from cache
// when a fetch happened within samlMetadataTTL. Inline XML config (no
// network fetch either way) is still cached, mainly to avoid re-parsing the
// XML on every login, not because it's expensive to skip.
func (s *SAMLAuthService) resolveIDPMetadata(ctx context.Context, tenantID uuid.UUID, cfg *domain.SAMLConfig) (*saml.EntityDescriptor, error) {
	s.metadataMu.Lock()
	cached, ok := s.metadataCache[tenantID]
	s.metadataMu.Unlock()
	if ok && time.Since(cached.fetchedAt) < samlMetadataTTL {
		return cached.entityDescriptor, nil
	}

	metadata, err := authn.ResolveIDPMetadata(ctx, authn.SAMLParams{
		IDPMetadataURL: cfg.IDPMetadataURL, IDPMetadataXML: cfg.IDPMetadataXML,
	})
	if err != nil {
		return nil, err
	}

	s.metadataMu.Lock()
	s.metadataCache[tenantID] = cachedSAMLMetadata{entityDescriptor: metadata, fetchedAt: time.Now()}
	s.metadataMu.Unlock()
	return metadata, nil
}

func (s *SAMLAuthService) loadConfig(ctx context.Context, tenantID uuid.UUID) (*domain.SAMLConfig, error) {
	var cfg *domain.SAMLConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.identityCfg.GetSAMLConfig(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

// buildServiceProvider resolves the tenant's stored SP keypair + IdP
// metadata into a live *saml.ServiceProvider. The keypair is re-resolved
// (via secrets.Store) on every call -- cheap, no network I/O for the
// EnvStore/Vault/KMS backends this app uses. IdP metadata goes through
// resolveIDPMetadata's cache instead of authn.BuildServiceProvider's
// uncached path, since ServeLogin and ServeACS would otherwise each pay a
// full network-fetch-plus-XML-parse on every single login.
func (s *SAMLAuthService) buildServiceProvider(ctx context.Context, tenantID uuid.UUID, cfg *domain.SAMLConfig) (*saml.ServiceProvider, error) {
	certPEM, err := s.secrets.Resolve(ctx, cfg.SPCertSecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve sp cert: %w", err)
	}
	keyPEM, err := s.secrets.Resolve(ctx, cfg.SPKeySecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve sp key: %w", err)
	}

	idpMetadata, err := s.resolveIDPMetadata(ctx, tenantID, cfg)
	if err != nil {
		return nil, err
	}

	return authn.BuildServiceProviderFromMetadata(authn.SAMLParams{
		EntityID: cfg.SPEntityID, ACSURL: cfg.ACSURL,
		CertPEM: certPEM, KeyPEM: keyPEM,
	}, idpMetadata)
}

func (s *SAMLAuthService) ServeMetadata(ctx context.Context, tenantID uuid.UUID, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig(ctx, tenantID)
	if err != nil || cfg == nil {
		http.Error(w, "saml is not configured for this tenant", http.StatusNotFound)
		return
	}
	sp, err := s.buildServiceProvider(ctx, tenantID, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	authn.ServeMetadata(sp, w)
}

func (s *SAMLAuthService) ServeLogin(ctx context.Context, tenantID uuid.UUID, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig(ctx, tenantID)
	if err != nil || cfg == nil {
		http.Error(w, "saml is not configured for this tenant", http.StatusNotFound)
		return
	}
	sp, err := s.buildServiceProvider(ctx, tenantID, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := authn.RedirectToIDP(sp, w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeACS handles the IdP's POSTed assertion, provisions/updates the user,
// and returns the session token as JSON. Production note: since the
// browser is mid-redirect from the IdP here, a real deployment should hand
// the token to the SPA via a same-origin postMessage or a short-lived
// one-time code exchanged over POST -- never as a URL query parameter,
// which proxies and browser history would log. Returning JSON directly
// (as done here) is a reasonable placeholder for that handoff, not the
// final production mechanism.
func (s *SAMLAuthService) ServeACS(ctx context.Context, tenantID uuid.UUID, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig(ctx, tenantID)
	if err != nil || cfg == nil {
		http.Error(w, "saml is not configured for this tenant", http.StatusNotFound)
		return
	}
	sp, err := s.buildServiceProvider(ctx, tenantID, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	groupAttr := ""
	if cfg.GroupAttribute != nil {
		groupAttr = *cfg.GroupAttribute
	}

	identity, err := authn.ParseAssertion(sp, w, r, groupAttr)
	if err != nil {
		http.Error(w, "invalid saml assertion", http.StatusUnauthorized)
		return
	}

	_, _, refreshToken, err := s.auth.ProvisionFederated(ctx, tenantID, domain.AuthProviderSAML, identity.NameID, identity.NameID, identity.Name, identity.Groups)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set the refresh cookie and redirect -- no token of any kind in this
	// response.
	//
	// This used to answer the IdP's POST with a JSON body containing a live
	// session token, which put a credential somewhere it did not belong:
	// browser history, any proxy log along the way, and the rendered page
	// itself if the redirect never happened. The documented fix was for the
	// SPA to exchange a single-use code, but a code is exactly what the
	// refresh cookie already is, only better -- HttpOnly, SameSite=Strict,
	// scoped to /auth, rotating on every use, revocable. So the browser is
	// simply sent to the app, where the SPA trades the cookie it cannot
	// read for an access token through the ordinary /auth/refresh call.
	//
	// SameSite=Strict is not a problem here: a cookie can still be SET on a
	// cross-site POST, and the /auth/refresh call that follows is the SPA
	// calling its own origin.
	sessioncookie.Set(w, refreshToken, s.secureCookies)
	http.Redirect(w, r, strings.TrimRight(s.appBaseURL, "/")+samlCallbackPath, http.StatusFound)
}
