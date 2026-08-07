package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// SAMLParams is authn's view of one tenant's SAML SP configuration —
// deliberately a plain struct, mirroring the LDAPParams pattern, so this
// package doesn't depend on internal/domain. internal/service maps
// domain.SAMLConfig (plus secrets resolved via secrets.Store) to this.
type SAMLParams struct {
	EntityID       string
	ACSURL         string
	IDPMetadataURL *string
	IDPMetadataXML *string
	CertPEM        string
	KeyPEM         string
}

// GenerateSPKeyPair creates a self-signed RSA keypair for a new tenant's
// SAML SP — called once from the Settings "Configure SAML" flow, not on
// every login. SAML SPs conventionally self-sign: the IdP trusts this
// certificate because an admin uploads/registers it out of band, not
// because a CA vouches for it.
func GenerateSPKeyPair(entityID string) (certPEM, keyPEM string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: entityID},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	return certPEM, keyPEM, nil
}

// ResolveIDPMetadata loads IdP metadata from either the stored XML (parsed
// fresh every call -- cheap, no network involved) or IDPMetadataURL
// (fetched over the network -- the expensive path). Split out from
// BuildServiceProvider so a caller that logs in frequently (SAMLAuthService)
// can cache the *saml.EntityDescriptor this returns instead of re-fetching
// it on every request -- see SAMLAuthService's metadata cache.
func ResolveIDPMetadata(ctx context.Context, p SAMLParams) (*saml.EntityDescriptor, error) {
	if p.IDPMetadataXML != nil && *p.IDPMetadataXML != "" {
		metadata, err := samlsp.ParseMetadata([]byte(*p.IDPMetadataXML))
		if err != nil {
			return nil, fmt.Errorf("load idp metadata: %w", err)
		}
		return metadata, nil
	}
	if p.IDPMetadataURL != nil && *p.IDPMetadataURL != "" {
		metadataURL, err := url.Parse(*p.IDPMetadataURL)
		if err != nil {
			return nil, fmt.Errorf("load idp metadata: %w", err)
		}
		metadata, err := samlsp.FetchMetadata(ctx, http.DefaultClient, *metadataURL)
		if err != nil {
			return nil, fmt.Errorf("load idp metadata: %w", err)
		}
		return metadata, nil
	}
	return nil, fmt.Errorf("no IdP metadata source configured")
}

// BuildServiceProviderFromMetadata builds the *saml.ServiceProvider from
// already-resolved IdP metadata, doing no network I/O itself -- the half of
// BuildServiceProvider that's cheap to run on every login.
func BuildServiceProviderFromMetadata(p SAMLParams, idpMetadata *saml.EntityDescriptor) (*saml.ServiceProvider, error) {
	certBlock, _ := pem.Decode([]byte(p.CertPEM))
	if certBlock == nil {
		return nil, fmt.Errorf("invalid sp certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse sp certificate: %w", err)
	}

	keyBlock, _ := pem.Decode([]byte(p.KeyPEM))
	if keyBlock == nil {
		return nil, fmt.Errorf("invalid sp key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse sp key: %w", err)
	}

	acsURL, err := url.Parse(p.ACSURL)
	if err != nil {
		return nil, fmt.Errorf("parse acs url: %w", err)
	}

	return &saml.ServiceProvider{
		EntityID:    p.EntityID,
		Key:         key,
		Certificate: cert,
		AcsURL:      *acsURL,
		IDPMetadata: idpMetadata,
	}, nil
}

// BuildServiceProvider constructs a *saml.ServiceProvider from stored
// config, resolving IdP metadata fresh on every call (fetching over the
// network if only IDPMetadataURL is configured). Callers that build a
// service provider frequently -- e.g. on every login -- should call
// ResolveIDPMetadata once (with their own caching) and
// BuildServiceProviderFromMetadata instead; this function is the
// uncached convenience wrapper of the two.
func BuildServiceProvider(ctx context.Context, p SAMLParams) (*saml.ServiceProvider, error) {
	idpMetadata, err := ResolveIDPMetadata(ctx, p)
	if err != nil {
		return nil, err
	}
	return BuildServiceProviderFromMetadata(p, idpMetadata)
}

// samlRequestIDCookie carries the AuthnRequest ID from RedirectToIDP to
// ParseAssertion so the ACS handler can check the response's InResponseTo
// against the request WE actually sent, rather than accepting any
// validly-signed assertion (which is what AllowIDPInitiated=true would do,
// trading away replay/CSRF protection on the login flow). The cookie value
// is an opaque, unguessable ID; forging it only helps an attacker if they
// can also forge a matching assertion signed by the trusted IdP, which is
// the real security boundary here — no HMAC signing needed on the cookie.
const samlRequestIDCookie = "argusops_saml_req"

// RedirectToIDP starts the SP-initiated login flow: builds an
// AuthnRequest, remembers its ID for the later ACS check, and redirects the
// browser to the IdP's SSO endpoint via the HTTP-Redirect binding.
func RedirectToIDP(sp *saml.ServiceProvider, w http.ResponseWriter, r *http.Request, relayState string) error {
	req, err := sp.MakeAuthenticationRequest(sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return fmt.Errorf("make authentication request: %w", err)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     samlRequestIDCookie,
		Value:    req.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   5 * 60,
	})

	redirectURL, err := req.Redirect(relayState, sp)
	if err != nil {
		return fmt.Errorf("build redirect: %w", err)
	}
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
	return nil
}

// ServeMetadata writes this SP's metadata document — the admin registers
// its URL (or downloads and uploads the XML) on the IdP side when setting
// up the integration.
func ServeMetadata(sp *saml.ServiceProvider, w http.ResponseWriter) {
	buf, err := xml.MarshalIndent(sp.Metadata(), "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("marshal metadata: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write(buf)
}

// SAMLIdentity is what the ACS handler extracts from a verified assertion —
// service.SAMLAuthService maps this to AuthService.ProvisionFederated the
// same way LDAPResult does for the LDAP flow.
type SAMLIdentity struct {
	NameID string
	Name   string
	Groups []string
}

// ParseAssertion validates the POSTed SAML response (signature, audience,
// timing, and that InResponseTo matches the AuthnRequest ID stashed by
// RedirectToIDP — see saml.ServiceProvider.ParseResponse) and extracts
// identity + group attributes. groupAttributeName selects which assertion
// attribute carries group/role membership for AuthGroupMapping matching —
// the SAML analogue of LDAPParams.GroupAttribute.
func ParseAssertion(sp *saml.ServiceProvider, w http.ResponseWriter, r *http.Request, groupAttributeName string) (*SAMLIdentity, error) {
	var possibleRequestIDs []string
	if cookie, err := r.Cookie(samlRequestIDCookie); err == nil && cookie.Value != "" {
		possibleRequestIDs = []string{cookie.Value}
		http.SetCookie(w, &http.Cookie{Name: samlRequestIDCookie, Value: "", Path: "/", MaxAge: -1})
	}

	assertion, err := sp.ParseResponse(r, possibleRequestIDs)
	if err != nil {
		return nil, fmt.Errorf("parse saml response: %w", err)
	}
	if assertion.Subject == nil || assertion.Subject.NameID == nil {
		return nil, fmt.Errorf("assertion has no NameID")
	}

	identity := &SAMLIdentity{NameID: assertion.Subject.NameID.Value, Name: assertion.Subject.NameID.Value}

	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			values := make([]string, 0, len(attr.Values))
			for _, v := range attr.Values {
				values = append(values, v.Value)
			}
			if attr.Name == groupAttributeName || attr.FriendlyName == groupAttributeName {
				identity.Groups = append(identity.Groups, values...)
			}
			if attr.Name == "displayName" || attr.FriendlyName == "displayName" {
				if len(values) > 0 && values[0] != "" {
					identity.Name = values[0]
				}
			}
		}
	}

	return identity, nil
}
