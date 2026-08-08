package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"sync"

	"github.com/argusops/argusops/internal/db"
)

// PersistentEnvStore is the production-safe default "env" backend: the same
// Put/Resolve contract as EnvStore, but every Put also writes an
// AES-256-GCM-encrypted copy to the secret_store table
// (db/migrations/0033_secret_store.up.sql), and NewPersistentEnvStore loads
// every existing row back into memory at construction. This is what fixes
// the failure mode EnvStore's own doc comment warns about: this app runs
// each cmd/* binary as a single instance that restarts on every deploy, and
// a bare in-memory map silently dropped every LDAP bind password / SAML SP
// keypair / LLM API key / webhook secret on each restart while the
// Postgres row referencing it (via a ref string) kept pointing at the now-
// empty value -- integrations then fail with generic-looking auth errors
// until an admin happens to notice and re-enter the secret. The encryption
// key (SECRETS_ENCRYPTION_KEY) never touches Postgres, so a database
// dump/backup alone still doesn't hand out a tenant's secrets.
type PersistentEnvStore struct {
	mu     sync.RWMutex
	values map[string]string
	pool   *db.Pool
	aead   cipher.AEAD
}

// NewPersistentEnvStore decodes encryptionKeyB64 (must be the base64 of
// exactly 32 raw bytes -- AES-256), then loads every currently-stored ref
// into memory so Resolve never needs a round-trip after startup.
func NewPersistentEnvStore(ctx context.Context, pool *db.Pool, encryptionKeyB64 string) (*PersistentEnvStore, error) {
	key, err := base64.StdEncoding.DecodeString(encryptionKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode SECRETS_ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("SECRETS_ENCRYPTION_KEY must decode to 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}

	s := &PersistentEnvStore{values: map[string]string{}, pool: pool, aead: aead}
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *PersistentEnvStore) load(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `select ref, nonce, ciphertext from secret_store`)
	if err != nil {
		return fmt.Errorf("load secret store: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ref string
		var nonce, ciphertext []byte
		if err := rows.Scan(&ref, &nonce, &ciphertext); err != nil {
			return fmt.Errorf("scan secret store row: %w", err)
		}
		plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return fmt.Errorf("decrypt secret %q: %w", ref, err)
		}
		s.values[ref] = string(plaintext)
	}
	return rows.Err()
}

func (s *PersistentEnvStore) Put(ctx context.Context, tenantID, purpose, value string) (string, error) {
	ref := tenantID + ":" + purpose

	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, []byte(value), nil)

	if _, err := s.pool.Exec(ctx, `
		insert into secret_store (ref, nonce, ciphertext, updated_at)
		values ($1, $2, $3, now())
		on conflict (ref) do update set nonce = $2, ciphertext = $3, updated_at = now()`,
		ref, nonce, ciphertext,
	); err != nil {
		return "", fmt.Errorf("persist secret: %w", err)
	}

	s.mu.Lock()
	s.values[ref] = value
	s.mu.Unlock()
	return ref, nil
}

func (s *PersistentEnvStore) Resolve(ctx context.Context, ref string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[ref], nil
}
