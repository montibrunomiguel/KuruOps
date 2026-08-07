package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// VaultStore backs Store with HashiCorp Vault's KV v2 secrets engine,
// talking to Vault's HTTP API directly via net/http rather than pulling in
// the official hashicorp/vault/api SDK -- this codebase already prefers a
// thin stdlib client over a heavy vendor SDK wherever the wire protocol is
// simple (see internal/llmclient's hand-rolled OpenAI/Anthropic clients),
// and KV v2's read/write is exactly that simple. Nothing here has been
// exercised against a real Vault server (none was available while
// building this) -- treat it as a solid starting point to validate before
// relying on it for anything customer-facing.
type VaultStore struct {
	addr  string
	token string
	// mount is the KV v2 engine's mount path (data lives under
	// <mount>/data/<path>), not the path to an individual secret.
	mount string
	http  *http.Client
}

func NewVaultStore(addr, token, mount string) *VaultStore {
	return &VaultStore{
		addr: strings.TrimSuffix(addr, "/"), token: token, mount: mount,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// Put writes value to <mount>/data/<tenantID>/<purpose> and returns that
// path as the ref -- Vault's own access control (policies on the token)
// is the actual security boundary; the path itself isn't a secret.
func (s *VaultStore) Put(ctx context.Context, tenantID, purpose, value string) (string, error) {
	path := tenantID + "/" + url.PathEscape(purpose)

	reqBody, err := json.Marshal(map[string]any{"data": map[string]string{"value": value}})
	if err != nil {
		return "", fmt.Errorf("encode vault request: %w", err)
	}
	if err := s.do(ctx, http.MethodPost, path, reqBody, nil); err != nil {
		return "", fmt.Errorf("vault write: %w", err)
	}
	return path, nil
}

// Resolve reads back the value written by Put. An empty ref resolves to ""
// (see AWSKMSStore.Resolve's identical convention).
func (s *VaultStore) Resolve(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}

	var out struct {
		Data struct {
			Data struct {
				Value string `json:"value"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet, ref, nil, &out); err != nil {
		return "", fmt.Errorf("vault read: %w", err)
	}
	return out.Data.Data.Value, nil
}

func (s *VaultStore) do(ctx context.Context, method, path string, reqBody []byte, out any) error {
	u := fmt.Sprintf("%s/v1/%s/data/%s", s.addr, s.mount, path)

	var bodyReader io.Reader
	if reqBody != nil {
		bodyReader = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", s.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
