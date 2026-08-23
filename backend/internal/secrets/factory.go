package secrets

import (
	"context"
	"fmt"

	"github.com/argusops/argusops/internal/config"
	"github.com/argusops/argusops/internal/db"
)

// NewFromConfig builds the Store for cfg.SecretsBackend -- shared by every
// cmd/* binary that resolves a secret (cmd/api for login/LLM/MCP secrets,
// cmd/worker for escalation destination secrets), so "vault"/"kms" only
// need to be wired up once. "env" (the default) is PersistentEnvStore --
// values are encrypted with SECRETS_ENCRYPTION_KEY and persisted to
// Postgres (secret_store table) so they survive a process restart, unlike
// the plain in-memory EnvStore this used to construct (see EnvStore's doc
// comment for the failure mode that fixed). A real deployment sets
// SECRETS_BACKEND explicitly, so a missing required value fails startup
// loudly rather than silently falling back to the insecure default.
// exampleSecretsEncryptionKey is the value committed in .env.example -- a
// real, working, base64-encoded 32-byte key so `docker compose up` works
// out of the box for local dev. That same convenience makes it a known
// shared key if it ever gets copy-pasted straight into a real deployment
// instead of generated fresh, so newFromConfig refuses to start with it
// outside a dev AuthMode.
const exampleSecretsEncryptionKey = "k83Yh5rQGCdVDMbUtJywr5J6ScE6++Bp4m936YZDecw="

func NewFromConfig(ctx context.Context, cfg config.Config, pool *db.Pool) (Store, error) {
	switch cfg.SecretsBackend {
	case "", "env":
		if cfg.SecretsEncryptionKey == "" {
			return nil, fmt.Errorf("SECRETS_ENCRYPTION_KEY is required (base64 of 32 random bytes) -- the default \"env\" backend persists secrets to Postgres, encrypted with this key, so LDAP/SAML/LLM/webhook secrets survive a restart instead of silently vanishing")
		}
		if cfg.SecretsEncryptionKey == exampleSecretsEncryptionKey && cfg.AuthMode != "dev" && cfg.AuthMode != "dev-headers" {
			return nil, fmt.Errorf("SECRETS_ENCRYPTION_KEY is still set to the .env.example default -- generate a real key (openssl rand -base64 32) before running outside AUTH_MODE=dev")
		}
		return NewPersistentEnvStore(ctx, pool, cfg.SecretsEncryptionKey)
	case "vault":
		if cfg.VaultAddr == "" || cfg.VaultToken == "" {
			return nil, fmt.Errorf("SECRETS_BACKEND=vault requires VAULT_ADDR and VAULT_TOKEN")
		}
		return NewVaultStore(cfg.VaultAddr, cfg.VaultToken, cfg.VaultMount), nil
	case "kms":
		if cfg.KMSRegion == "" || cfg.KMSAccessKeyID == "" || cfg.KMSSecretAccessKey == "" || cfg.KMSKeyID == "" {
			return nil, fmt.Errorf("SECRETS_BACKEND=kms requires KMS_REGION, KMS_ACCESS_KEY_ID, KMS_SECRET_ACCESS_KEY, and KMS_KEY_ID")
		}
		return NewAWSKMSStore(cfg.KMSRegion, cfg.KMSAccessKeyID, cfg.KMSSecretAccessKey, cfg.KMSKeyID), nil
	default:
		return nil, fmt.Errorf("unknown SECRETS_BACKEND %q (want env, vault, or kms)", cfg.SecretsBackend)
	}
}
