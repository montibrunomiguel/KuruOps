package secrets

import (
	"fmt"

	"github.com/argusops/argusops/internal/config"
)

// NewFromConfig builds the Store for cfg.SecretsBackend -- shared by every
// cmd/* binary that resolves a secret (cmd/api for login/LLM/MCP secrets,
// cmd/worker for escalation destination secrets), so "vault"/"kms" only
// need to be wired up once. "env" (the default) needs no further config --
// it's the in-memory dev-only stub, see EnvStore's doc comment for why it
// must never run this way outside development. A real deployment sets
// SECRETS_BACKEND explicitly, so a missing required value fails startup
// loudly rather than silently falling back to the insecure default.
func NewFromConfig(cfg config.Config) (Store, error) {
	switch cfg.SecretsBackend {
	case "", "env":
		return NewEnvStore(), nil
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
