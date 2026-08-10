package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/argusops/argusops/internal/db"
)

// refreshInterval is how often a running PersistentEnvStore reloads its
// entire in-memory map from Postgres -- see the periodic-refresh goroutine
// started in NewPersistentEnvStore. Bounds how long a secret rotated on one
// replica can stay stale on another. A var, not a const, only so
// persistent_store_internal_test.go can shrink it to keep the cross-replica
// propagation test fast -- production code never reassigns it.
var refreshInterval = 60 * time.Second

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
//
// Resolve only ever reads the local in-memory map, never Postgres directly
// -- fine for a single replica (Put keeps its own cache current), but with
// more than one api/ingest replica, a secret rotated via Put on replica A
// was previously invisible to replica B until B happened to restart: no
// error, just a stale or missing value used for the next LLM call/LDAP
// bind/webhook send. The periodic refresh below (see refreshInterval)
// bounds that staleness window instead of leaving it open indefinitely;
// Vault/KMS backends don't have this problem at all since they hit their
// backend on every Resolve call rather than caching -- a full re-fetch
// every call was judged not worth the added latency here, given Resolve is
// on paths like every LLM call and every escalation send.
type PersistentEnvStore struct {
	mu     sync.RWMutex
	values map[string]string
	pool   *db.Pool
	aead   cipher.AEAD
}

// NewPersistentEnvStore decodes encryptionKeyB64 (must be the base64 of
// exactly 32 raw bytes -- AES-256), loads every currently-stored ref into
// memory so Resolve never needs a round-trip after startup, then starts a
// background goroutine that reloads the whole map every refreshInterval
// (see PersistentEnvStore's doc comment) until ctx is cancelled -- callers
// already pass the process's root shutdown context here (see
// secrets.NewFromConfig's callers in cmd/api, cmd/ingest, cmd/worker), so
// this ties the refresh loop's lifetime to normal process shutdown with no
// separate Close() method to wire through three different main()s.
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
	values, err := s.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	s.values = values

	go s.refreshLoop(ctx)
	return s, nil
}

// refreshLoop reloads the whole map every refreshInterval until ctx is
// cancelled. A fetch error is logged and skipped rather than fatal -- a
// transient DB hiccup here shouldn't crash a long-running process; Resolve
// just keeps serving whatever it last successfully loaded until the next
// tick succeeds.
func (s *PersistentEnvStore) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			values, err := s.fetchAll(ctx)
			if err != nil {
				slog.Error("secrets: periodic refresh failed, keeping previous values", "error", err)
				continue
			}
			s.mu.Lock()
			s.values = values
			s.mu.Unlock()
		}
	}
}

// fetchAll reads and decrypts every row in secret_store into a fresh map --
// built separately and swapped in wholesale (see refreshLoop/
// NewPersistentEnvStore) rather than mutated in place, so a ref deleted
// server-side since the last load also disappears here instead of lingering
// forever in a merge.
func (s *PersistentEnvStore) fetchAll(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `select ref, nonce, ciphertext from secret_store`)
	if err != nil {
		return nil, fmt.Errorf("load secret store: %w", err)
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var ref string
		var nonce, ciphertext []byte
		if err := rows.Scan(&ref, &nonce, &ciphertext); err != nil {
			return nil, fmt.Errorf("scan secret store row: %w", err)
		}
		plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %q: %w", ref, err)
		}
		values[ref] = string(plaintext)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
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
