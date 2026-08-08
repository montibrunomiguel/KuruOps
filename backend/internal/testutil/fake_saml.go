package testutil

import (
	"strings"
	"testing"
)

// IDPMetadataXML builds a minimal, well-formed SAML 2.0 IdP metadata
// document embedding certPEM as its signing certificate -- enough for
// samlsp.ParseMetadata (called by authn.BuildServiceProvider) without
// needing a live IdP or network access. certPEM would typically come from
// authn.GenerateSPKeyPair (it only generates a self-signed cert, no real
// CA involvement, so it's equally usable to stand in for an IdP's cert in
// tests).
func IDPMetadataXML(t *testing.T, entityID, certPEM string) string {
	t.Helper()
	certDER := strings.TrimSpace(certPEM)
	certDER = strings.TrimPrefix(certDER, "-----BEGIN CERTIFICATE-----")
	certDER = strings.TrimSuffix(certDER, "-----END CERTIFICATE-----")
	certDER = strings.TrimSpace(certDER)

	return `<?xml version="1.0" encoding="UTF-8"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="` + entityID + `">
  <IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">
        <ds:X509Data>
          <ds:X509Certificate>` + certDER + `</ds:X509Certificate>
        </ds:X509Data>
      </ds:KeyInfo>
    </KeyDescriptor>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example/sso"/>
  </IDPSSODescriptor>
</EntityDescriptor>`
}
