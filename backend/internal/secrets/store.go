// Package secrets defines the boundary between ArgusOps and wherever LLM
// API keys / MCP auth material actually live. Postgres only ever stores a
// reference string (llm_providers.api_key_secret_ref,
// mcp_servers.auth_secret_ref) — never the secret itself — so a database
// dump or a leaked backup does not hand out a tenant's OpenAI/Anthropic key.
package secrets

import (
	"context"
	"sync"
)

// Store puts a secret value and gets back a reference to store in Postgres,
// or resolves a previously stored reference back to its value. Production
// implementations back this with Vault, AWS/GCP Secrets Manager, or
// Kubernetes Secrets synced via External Secrets Operator — see the
// architecture review, section "Kubernetes", for the recommended setup.
type Store interface {
	Put(ctx context.Context, tenantID, purpose, value string) (ref string, err error)
	Resolve(ctx context.Context, ref string) (value string, err error)
}

// EnvStore is a pure in-memory Store, kept around for tests that need a
// Store without a database connection. It is NOT what any cmd/* binary
// constructs at runtime any more -- secrets.NewFromConfig's default "env"
// backend is PersistentEnvStore (persistent_store.go), which persists the
// same Put/Resolve contract to Postgres precisely because this type's
// values don't survive a process restart. One EnvStore is shared across
// every request in tests that use it, so Put/Resolve need their own
// locking -- a plain map is not safe for the concurrent goroutine-per-
// request access pattern net/http gives every handler.
type EnvStore struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewEnvStore() *EnvStore {
	return &EnvStore{values: map[string]string{}}
}

func (s *EnvStore) Put(ctx context.Context, tenantID, purpose, value string) (string, error) {
	ref := tenantID + ":" + purpose
	s.mu.Lock()
	s.values[ref] = value
	s.mu.Unlock()
	return ref, nil
}

func (s *EnvStore) Resolve(ctx context.Context, ref string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[ref], nil
}
