package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/mcpclient"
)

// MCPServerAuthInput is the authentication half of MCPServerSaveInput. Which
// fields apply depends on Type:
//
//	none     -- nothing else
//	api_key  -- APIKeyHeader + APIKey
//	bearer   -- BearerToken
//	oauth    -- OAuthTokenURL + OAuthClientID + OAuthClientSecret (client_credentials)
//
// Every secret field is plaintext on the way in, goes straight into
// secrets.Store, and is never read back out through the API.
//
// On update an empty Type means "leave authentication exactly as it is" -- the
// Discover Tools panel saves the tool lists without knowing (or being able to
// know) the credentials. Sending a secret field without a Type is an error
// rather than a silent no-op, so a rotation can't be dropped by accident.
type MCPServerAuthInput struct {
	Type              string
	APIKeyHeader      string
	APIKey            string
	BearerToken       string
	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
}

func (a MCPServerAuthInput) hasFields() bool {
	return a.APIKeyHeader != "" || a.APIKey != "" || a.BearerToken != "" ||
		a.OAuthTokenURL != "" || a.OAuthClientID != "" || a.OAuthClientSecret != ""
}

const maxClientIDLen = 256

// Secret-store purposes. One per kind of secret so two never overwrite each
// other (PersistentEnvStore and Vault key by purpose alone). Bearer keeps the
// historical "mcp:<name>" purpose, so a server created before auth types
// existed -- migrated to bearer -- is rotated in place.
func mcpBearerPurpose(name string) string      { return "mcp:" + name }
func mcpAPIKeyPurpose(name string) string      { return "mcp:" + name + ":api-key" }
func mcpOAuthSecretPurpose(name string) string { return "mcp:" + name + ":oauth-client-secret" }

// applyAuth validates in and writes the resulting authentication columns onto
// target, storing any new secret. prev is the server's state before this save
// (nil on create). target's endpoint/name must already be set. It reports
// whether a new credential was stored, for the audit trail.
//
// The rule that matters most for security: a stored secret can be kept (the
// form never round-trips it, so "blank" has to mean "keep") only while it still
// goes to the destination it was saved for. Change the endpoint -- or, for
// OAuth, the token URL or client id -- and the secret must be entered again.
// Otherwise any admin could repoint a server at a host they control, leave the
// password field empty, and have the platform deliver a credential they were
// never allowed to read.
func (s *MCPServerService) applyAuth(ctx context.Context, tenantID uuid.UUID, target, prev *domain.MCPServer, in MCPServerAuthInput) (rotated bool, err error) {
	authType := in.Type
	if authType == "" {
		if in.hasFields() {
			return false, errors.New("authType is required when any authentication field is set")
		}
		if prev == nil {
			authType = domain.MCPAuthNone
		} else {
			// Unchanged -- but only valid while the destination is too.
			if prev.AuthType != "" && prev.AuthType != domain.MCPAuthNone && prev.EndpointOrCommand != target.EndpointOrCommand {
				return false, errCredentialNotPortable
			}
			copyAuth(target, prev)
			if target.AuthType == "" {
				target.AuthType = domain.MCPAuthNone
			}
			return false, nil
		}
	}

	endpointChanged := prev != nil && prev.EndpointOrCommand != target.EndpointOrCommand
	// canKeep: the stored secret of type t may be reused as-is.
	canKeep := func(t string, ref *string) bool {
		return prev != nil && prev.AuthType == t && ref != nil && !endpointChanged
	}
	clearAuth(target)

	switch authType {
	case domain.MCPAuthNone:
		if in.hasFields() {
			return false, errors.New("no authentication fields may be set when authType is none")
		}

	case domain.MCPAuthAPIKey:
		if in.BearerToken != "" || in.OAuthTokenURL != "" || in.OAuthClientID != "" || in.OAuthClientSecret != "" {
			return false, errors.New("only apiKeyHeader and apiKey apply when authType is api_key")
		}
		header := strings.TrimSpace(in.APIKeyHeader)
		if err := mcpclient.ValidateHeaderName(header); err != nil {
			return false, fmt.Errorf("apiKeyHeader: %w", err)
		}
		old := prevRef(prev, func(p *domain.MCPServer) *string { return p.AuthSecretRef })
		ref, stored, err := s.secretFor(ctx, tenantID, mcpAPIKeyPurpose(target.Name), strings.TrimSpace(in.APIKey), "apiKey",
			canKeep(domain.MCPAuthAPIKey, old), old)
		if err != nil {
			return false, err
		}
		target.AuthType, target.AuthHeaderName, target.AuthSecretRef = domain.MCPAuthAPIKey, &header, ref
		return stored, nil

	case domain.MCPAuthBearer:
		if in.APIKeyHeader != "" || in.APIKey != "" || in.OAuthTokenURL != "" || in.OAuthClientID != "" || in.OAuthClientSecret != "" {
			return false, errors.New("only bearerToken applies when authType is bearer")
		}
		old := prevRef(prev, func(p *domain.MCPServer) *string { return p.AuthSecretRef })
		ref, stored, err := s.secretFor(ctx, tenantID, mcpBearerPurpose(target.Name), strings.TrimSpace(in.BearerToken), "bearerToken",
			canKeep(domain.MCPAuthBearer, old), old)
		if err != nil {
			return false, err
		}
		target.AuthType, target.AuthSecretRef = domain.MCPAuthBearer, ref
		return stored, nil

	case domain.MCPAuthOAuth:
		if in.APIKeyHeader != "" || in.APIKey != "" || in.BearerToken != "" {
			return false, errors.New("only oauthTokenUrl, oauthClientId and oauthClientSecret apply when authType is oauth")
		}
		tokenURL := strings.TrimSpace(in.OAuthTokenURL)
		if err := validateOAuthTokenURL(tokenURL); err != nil {
			return false, fmt.Errorf("oauthTokenUrl: %w", err)
		}
		clientID := strings.TrimSpace(in.OAuthClientID)
		if err := mcpclient.ValidateCredential(clientID); err != nil || len(clientID) > maxClientIDLen {
			return false, errors.New("oauthClientId is required (and must be printable text of at most 256 characters)")
		}
		old := prevRef(prev, func(p *domain.MCPServer) *string { return p.OAuthClientSecretRef })
		sameClient := prev != nil && deref(prev.OAuthTokenURL) == tokenURL && deref(prev.OAuthClientID) == clientID
		ref, stored, err := s.secretFor(ctx, tenantID, mcpOAuthSecretPurpose(target.Name), strings.TrimSpace(in.OAuthClientSecret), "oauthClientSecret",
			canKeep(domain.MCPAuthOAuth, old) && sameClient, old)
		if err != nil {
			return false, err
		}
		target.AuthType, target.OAuthTokenURL, target.OAuthClientID, target.OAuthClientSecretRef = domain.MCPAuthOAuth, &tokenURL, &clientID, ref
		return stored, nil

	default:
		return false, fmt.Errorf("authType must be one of none, api_key, bearer, oauth; got %q", authType)
	}
	return false, nil
}

var errCredentialNotPortable = errors.New(
	"the stored credential can't be reused with a different endpoint: send authType and the credential again")

// secretFor returns the ref to store: a freshly Put one when plaintext is
// given, the existing one when keeping is allowed, otherwise an error naming
// the field to fill in.
func (s *MCPServerService) secretFor(ctx context.Context, tenantID uuid.UUID, purpose, plaintext, field string, keep bool, existingRef *string) (ref *string, stored bool, err error) {
	if plaintext == "" {
		if keep {
			return existingRef, false, nil
		}
		return nil, false, fmt.Errorf("%s is required (a stored credential is only kept for the same endpoint and auth type it was saved with)", field)
	}
	if err := mcpclient.ValidateCredential(plaintext); err != nil {
		return nil, false, fmt.Errorf("%s: %w", field, err)
	}
	r, err := s.secrets.Put(ctx, tenantID.String(), purpose, plaintext)
	if err != nil {
		return nil, false, fmt.Errorf("store mcp %s: %w", field, err)
	}
	return &r, true, nil
}

func prevRef(prev *domain.MCPServer, pick func(*domain.MCPServer) *string) *string {
	if prev == nil {
		return nil
	}
	return pick(prev)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func clearAuth(s *domain.MCPServer) {
	s.AuthType = domain.MCPAuthNone
	s.AuthHeaderName, s.AuthSecretRef = nil, nil
	s.OAuthTokenURL, s.OAuthClientID, s.OAuthClientSecretRef = nil, nil, nil
}

func copyAuth(dst, src *domain.MCPServer) {
	dst.AuthType, dst.AuthHeaderName, dst.AuthSecretRef = src.AuthType, src.AuthHeaderName, src.AuthSecretRef
	dst.OAuthTokenURL, dst.OAuthClientID, dst.OAuthClientSecretRef = src.OAuthTokenURL, src.OAuthClientID, src.OAuthClientSecretRef
}

// validateOAuthTokenURL checks the authorization server's token endpoint. The
// client secret is sent here, so unlike an MCP endpoint it must be https --
// plain http is accepted only for a loopback host (local development; at
// request time httpguard still refuses loopback unless explicitly allowed).
// Credentials embedded in the URL are rejected: they would be persisted in
// plaintext in the database and in the audit log, bypassing secrets.Store.
//
// Like validateEndpoint this is input hygiene, not the SSRF control -- that is
// httpguard, at dial time.
func validateOAuthTokenURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	if len(raw) > 2048 {
		return errors.New("is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("must be a valid URL with a host")
	}
	if u.User != nil {
		return errors.New("must not contain embedded credentials")
	}
	if u.Fragment != "" {
		return errors.New("must not contain a fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return errors.New("must use https (http is only accepted for localhost)")
		}
	default:
		return errors.New("must be an https:// URL")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
