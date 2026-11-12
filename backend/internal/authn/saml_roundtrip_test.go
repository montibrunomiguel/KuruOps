package authn_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
)

// The tests below drive a real signed SAML response, issued by the library's
// own IdentityProvider, through authn.ParseAssertion. They exist so that a
// bump of github.com/crewjam/saml -- whose validation has been getting
// stricter -- fails here rather than at the first login of a real IdP user.

const (
	rtACSURL = "https://kuruops.example/auth/saml/acs"
	rtIDPURL = "https://idp.example/metadata"
)

type rtSPProvider struct{ md *saml.EntityDescriptor }

func (p rtSPProvider) GetServiceProvider(*http.Request, string) (*saml.EntityDescriptor, error) {
	return p.md, nil
}

type rtSession struct{ s *saml.Session }

func (p rtSession) GetSession(http.ResponseWriter, *http.Request, *saml.IdpAuthnRequest) *saml.Session {
	return p.s
}

// rtMaker issues the default assertion, then lets a test mangle it before the
// IdP signs it -- the way a buggy or hostile (but trusted) IdP would.
type rtMaker struct{ mangle func(*saml.Assertion) }

func (m rtMaker) MakeAssertion(req *saml.IdpAuthnRequest, session *saml.Session) error {
	if err := (saml.DefaultAssertionMaker{}).MakeAssertion(req, session); err != nil {
		return err
	}
	if m.mangle != nil {
		m.mangle(req.Assertion)
	}
	return nil
}

var samlResponseField = regexp.MustCompile(`name="SAMLResponse"\s+value="([^"]*)"`)

// rtLogin runs the whole redirect -> IdP -> ACS dance and returns what
// ParseAssertion makes of the signed response. requestID overrides the
// RelayState the ACS is called with when non-empty.
func rtLogin(t *testing.T, mangle func(*saml.Assertion), requestID string) (*authn.SAMLIdentity, error) {
	t.Helper()

	spCert, spKey, err := authn.GenerateSPKeyPair("https://kuruops.example/saml/metadata")
	require.NoError(t, err)
	idpCertPEM, idpKeyPEM, err := authn.GenerateSPKeyPair(rtIDPURL)
	require.NoError(t, err)

	idpCertBlock, _ := pem.Decode([]byte(idpCertPEM))
	idpCert, err := x509.ParseCertificate(idpCertBlock.Bytes)
	require.NoError(t, err)
	idpKeyBlock, _ := pem.Decode([]byte(idpKeyPEM))
	idpKey, err := x509.ParsePKCS1PrivateKey(idpKeyBlock.Bytes)
	require.NoError(t, err)

	metadataURL, _ := url.Parse(rtIDPURL)
	ssoURL, _ := url.Parse("https://idp.example/sso")
	idp := &saml.IdentityProvider{
		Key:         idpKey,
		Certificate: idpCert,
		MetadataURL: *metadataURL,
		SSOURL:      *ssoURL,
		SessionProvider: rtSession{&saml.Session{
			ID:           "session-1",
			NameID:       "alice@example.com",
			NameIDFormat: "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
			CustomAttributes: []saml.Attribute{
				{Name: "groups", Values: []saml.AttributeValue{
					{Type: "xs:string", Value: "soc-admins"},
					{Type: "xs:string", Value: "soc-viewers"},
				}},
				{Name: "displayName", Values: []saml.AttributeValue{{Type: "xs:string", Value: "Alice Analyst"}}},
			},
		}},
		AssertionMaker: rtMaker{mangle},
	}

	idpMetadataXML, err := xml.Marshal(idp.Metadata())
	require.NoError(t, err)
	metadataXML := string(idpMetadataXML)
	sp, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
		EntityID:       "https://kuruops.example/saml/metadata",
		ACSURL:         rtACSURL,
		IDPMetadataXML: &metadataXML,
		CertPEM:        spCert,
		KeyPEM:         spKey,
	})
	require.NoError(t, err)
	idp.ServiceProviderProvider = rtSPProvider{sp.Metadata()}

	authnReq, err := sp.MakeAuthenticationRequest(
		sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	require.NoError(t, err)
	redirect, err := authnReq.Redirect(authnReq.ID, sp)
	require.NoError(t, err)

	sso := httptest.NewRecorder()
	idp.ServeSSO(sso, httptest.NewRequest(http.MethodGet, redirect.String(), nil))
	require.Equal(t, http.StatusOK, sso.Code, sso.Body.String())
	m := samlResponseField.FindStringSubmatch(sso.Body.String())
	require.Len(t, m, 2, "IdP did not return a SAMLResponse form: %s", sso.Body.String())

	relay := authnReq.ID
	if requestID != "" {
		relay = requestID
	}
	form := url.Values{"SAMLResponse": {html.UnescapeString(m[1])}, "RelayState": {relay}}
	acs := httptest.NewRequest(http.MethodPost, rtACSURL, strings.NewReader(form.Encode()))
	acs.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return authn.ParseAssertion(sp, httptest.NewRecorder(), acs, "groups")
}

func TestParseAssertion_SignedResponseRoundTrip(t *testing.T) {
	identity, err := rtLogin(t, nil, "")
	require.NoError(t, err)

	assert.Equal(t, "alice@example.com", identity.NameID)
	assert.Equal(t, "Alice Analyst", identity.Name, "displayName attribute wins over the NameID")
	assert.Equal(t, []string{"soc-admins", "soc-viewers"}, identity.Groups)
}

// A trusted IdP that signs an assertion missing a required element must get a
// clean error, never a nil-pointer panic in the ACS handler (GO-2026-6909).
func TestParseAssertion_SignedAssertionMissingRequiredElementsIsAnErrorNotAPanic(t *testing.T) {
	for name, mangle := range map[string]func(*saml.Assertion){
		"no Subject":    func(a *saml.Assertion) { a.Subject = nil },
		"no Conditions": func(a *saml.Assertion) { a.Conditions = nil },
		"no NameID":     func(a *saml.Assertion) { a.Subject.NameID = nil },
	} {
		t.Run(name, func(t *testing.T) {
			var identity *authn.SAMLIdentity
			var err error
			require.NotPanics(t, func() { identity, err = rtLogin(t, mangle, "") })
			assert.Error(t, err)
			assert.Nil(t, identity)
		})
	}
}

func TestParseAssertion_ResponseForAnotherRequestIsRejected(t *testing.T) {
	identity, err := rtLogin(t, nil, "id-that-this-sp-never-issued")
	assert.Error(t, err)
	assert.Nil(t, identity)
}
