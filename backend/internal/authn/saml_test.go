package authn_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestGenerateSPKeyPair(t *testing.T) {
	certPEM, keyPEM, err := authn.GenerateSPKeyPair("https://kuruops.example/saml/metadata")
	require.NoError(t, err)
	assert.Contains(t, certPEM, "BEGIN CERTIFICATE")
	assert.Contains(t, keyPEM, "BEGIN RSA PRIVATE KEY")
}

func TestBuildServiceProvider(t *testing.T) {
	spCert, spKey, err := authn.GenerateSPKeyPair("https://kuruops.example/saml/metadata")
	require.NoError(t, err)
	idpCert, _, err := authn.GenerateSPKeyPair("https://idp.example/metadata")
	require.NoError(t, err)
	metadataXML := testutil.IDPMetadataXML(t, "https://idp.example/metadata", idpCert)

	t.Run("valid config with inline IdP metadata XML", func(t *testing.T) {
		sp, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
			EntityID:       "https://kuruops.example/saml/metadata",
			ACSURL:         "https://kuruops.example/auth/saml/acs",
			IDPMetadataXML: &metadataXML,
			CertPEM:        spCert,
			KeyPEM:         spKey,
		})
		require.NoError(t, err)
		assert.Equal(t, "https://kuruops.example/saml/metadata", sp.EntityID)
		assert.Equal(t, "https://kuruops.example/auth/saml/acs", sp.AcsURL.String())
	})

	t.Run("invalid SP certificate PEM", func(t *testing.T) {
		_, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
			EntityID: "e", ACSURL: "https://kuruops.example/acs",
			IDPMetadataXML: &metadataXML,
			CertPEM:        "not a pem block",
			KeyPEM:         spKey,
		})
		assert.Error(t, err)
	})

	t.Run("invalid SP key PEM", func(t *testing.T) {
		_, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
			EntityID: "e", ACSURL: "https://kuruops.example/acs",
			IDPMetadataXML: &metadataXML,
			CertPEM:        spCert,
			KeyPEM:         "not a pem block",
		})
		assert.Error(t, err)
	})

	t.Run("malformed IdP metadata XML", func(t *testing.T) {
		bogus := "<not><valid saml metadata"
		_, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
			EntityID: "e", ACSURL: "https://kuruops.example/acs",
			IDPMetadataXML: &bogus,
			CertPEM:        spCert,
			KeyPEM:         spKey,
		})
		assert.Error(t, err)
	})

	t.Run("no IdP metadata source configured at all", func(t *testing.T) {
		_, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
			EntityID: "e", ACSURL: "https://kuruops.example/acs",
			CertPEM: spCert,
			KeyPEM:  spKey,
		})
		assert.Error(t, err)
	})

	t.Run("invalid ACS URL", func(t *testing.T) {
		_, err := authn.BuildServiceProvider(context.Background(), authn.SAMLParams{
			EntityID: "e", ACSURL: "://not a url",
			IDPMetadataXML: &metadataXML,
			CertPEM:        spCert,
			KeyPEM:         spKey,
		})
		assert.Error(t, err)
	})
}

// TestResolveIDPMetadata_BuildServiceProviderFromMetadata_MatchBuildServiceProvider
// guards the split introduced for SAMLAuthService's metadata cache
// (ResolveIDPMetadata does the network-fetch-or-XML-parse step,
// BuildServiceProviderFromMetadata does the rest) -- the two-step path must
// produce an equivalent *saml.ServiceProvider to the original single-call
// BuildServiceProvider, since ServeLogin/ServeACS now go through the split
// path with a cache in between.
func TestResolveIDPMetadata_BuildServiceProviderFromMetadata_MatchBuildServiceProvider(t *testing.T) {
	params := buildTestSP(t)

	metadata, err := authn.ResolveIDPMetadata(context.Background(), *params)
	require.NoError(t, err)
	require.NotNil(t, metadata)

	sp, err := authn.BuildServiceProviderFromMetadata(*params, metadata)
	require.NoError(t, err)

	want, err := authn.BuildServiceProvider(context.Background(), *params)
	require.NoError(t, err)

	assert.Equal(t, want.EntityID, sp.EntityID)
	assert.Equal(t, want.AcsURL.String(), sp.AcsURL.String())
	assert.Equal(t, want.IDPMetadata.EntityID, sp.IDPMetadata.EntityID)

	t.Run("invalid SP certificate PEM still errors on the metadata-provided path", func(t *testing.T) {
		_, err := authn.BuildServiceProviderFromMetadata(authn.SAMLParams{
			EntityID: "e", ACSURL: "https://kuruops.example/acs",
			CertPEM: "not a pem block", KeyPEM: params.KeyPEM,
		}, metadata)
		assert.Error(t, err)
	})
}

func TestResolveIDPMetadata_NoSourceConfigured(t *testing.T) {
	_, err := authn.ResolveIDPMetadata(context.Background(), authn.SAMLParams{})
	assert.Error(t, err)
}

func TestResolveIDPMetadata_MalformedXML(t *testing.T) {
	bogus := "<not><valid saml metadata"
	_, err := authn.ResolveIDPMetadata(context.Background(), authn.SAMLParams{IDPMetadataXML: &bogus})
	assert.Error(t, err)
}

func buildTestSP(t *testing.T) *authn.SAMLParams {
	t.Helper()
	spCert, spKey, err := authn.GenerateSPKeyPair("https://kuruops.example/saml/metadata")
	require.NoError(t, err)
	idpCert, _, err := authn.GenerateSPKeyPair("https://idp.example/metadata")
	require.NoError(t, err)
	metadataXML := testutil.IDPMetadataXML(t, "https://idp.example/metadata", idpCert)
	return &authn.SAMLParams{
		EntityID:       "https://kuruops.example/saml/metadata",
		ACSURL:         "https://kuruops.example/auth/saml/acs",
		IDPMetadataXML: &metadataXML,
		CertPEM:        spCert,
		KeyPEM:         spKey,
	}
}

func TestRedirectToIDP(t *testing.T) {
	sp, err := authn.BuildServiceProvider(context.Background(), *buildTestSP(t))
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "https://kuruops.example/auth/saml/login", nil)
	rec := httptest.NewRecorder()

	err = authn.RedirectToIDP(sp, rec, req)
	require.NoError(t, err)

	assert.Equal(t, 302, rec.Code)
	location := rec.Header().Get("Location")
	require.NotEmpty(t, location)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.NotEmpty(t, cookies[0].Value, "the AuthnRequest ID must be stashed in a cookie for the later ACS check")

	// RelayState must carry the same request ID as the cookie -- it's the
	// primary channel now, since a SameSite=Lax cookie is never sent on the
	// cross-site POST every real IdP uses to return the assertion (see
	// RedirectToIDP's doc comment).
	parsedLocation, err := url.Parse(location)
	require.NoError(t, err)
	assert.Equal(t, cookies[0].Value, parsedLocation.Query().Get("RelayState"))
}

func TestServeMetadata(t *testing.T) {
	sp, err := authn.BuildServiceProvider(context.Background(), *buildTestSP(t))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	authn.ServeMetadata(sp, rec)

	assert.Equal(t, "application/samlmetadata+xml", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "EntityDescriptor")
	assert.Contains(t, rec.Body.String(), "https://kuruops.example/saml/metadata")
}

func TestParseAssertion_RejectsMissingOrInvalidResponse(t *testing.T) {
	sp, err := authn.BuildServiceProvider(context.Background(), *buildTestSP(t))
	require.NoError(t, err)

	t.Run("no SAMLResponse in the POST body", func(t *testing.T) {
		req := httptest.NewRequest("POST", "https://kuruops.example/auth/saml/acs", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		_, err := authn.ParseAssertion(sp, rec, req, "groups")
		assert.Error(t, err)
	})

	t.Run("garbage SAMLResponse value", func(t *testing.T) {
		req := httptest.NewRequest("POST", "https://kuruops.example/auth/saml/acs", strings.NewReader("SAMLResponse=not-valid-base64%3D%3D"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		_, err := authn.ParseAssertion(sp, rec, req, "groups")
		assert.Error(t, err)
	})
}
