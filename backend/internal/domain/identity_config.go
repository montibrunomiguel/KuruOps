package domain

import (
	"time"

	"github.com/google/uuid"
)

// LDAPConfig mirrors `tenant_ldap_config` — one directory per tenant in v1.
// BindPasswordSecretRef points into the secret manager (internal/secrets);
// the service account password is never stored here in plaintext, and the
// ref itself is never returned by the API either since the frontend has no
// use for it.
type LDAPConfig struct {
	TenantID              uuid.UUID `json:"tenantId"`
	Host                  string    `json:"host"`
	Port                  int       `json:"port"`
	UseTLS                bool      `json:"useTls"`
	BindDN                string    `json:"bindDn"`
	BindPasswordSecretRef string    `json:"-"`
	UserBaseDN            string    `json:"userBaseDn"`
	UserFilter            string    `json:"userFilter"`
	GroupBaseDN           string    `json:"groupBaseDn"`
	GroupAttribute        string    `json:"groupAttribute"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

// SAMLConfig mirrors `tenant_saml_config`. SPCertSecretRef/SPKeySecretRef
// point into the secret manager — the SP's signing keypair is generated
// once per tenant and never stored in plaintext in Postgres, nor returned
// by the API.
type SAMLConfig struct {
	TenantID        uuid.UUID `json:"tenantId"`
	IDPMetadataURL  *string   `json:"idpMetadataUrl,omitempty"`
	IDPMetadataXML  *string   `json:"idpMetadataXml,omitempty"`
	SPEntityID      string    `json:"spEntityId"`
	ACSURL          string    `json:"acsUrl"`
	SPCertSecretRef string    `json:"-"`
	SPKeySecretRef  string    `json:"-"`
	GroupAttribute  *string   `json:"groupAttribute,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
