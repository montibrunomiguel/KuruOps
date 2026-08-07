package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/argusops/argusops/internal/domain"
)

// CorrelationEngine evaluates incoming alerts to detect duplicates and aggregate
// high-frequency security events within a sliding window.
type CorrelationEngine struct {
	mu           sync.RWMutex
	window       time.Duration
	fingerprints map[string]time.Time
}

// NewCorrelationEngine creates a correlation engine with a deduplication time window.
func NewCorrelationEngine(window time.Duration) *CorrelationEngine {
	if window <= 0 {
		window = 15 * time.Minute // default 15 minutes window
	}
	return &CorrelationEngine{
		window:       window,
		fingerprints: make(map[string]time.Time),
	}
}

// ComputeFingerprint generates a deterministic SHA-256 hash based on core alert fields.
func (ce *CorrelationEngine) ComputeFingerprint(alert domain.Alert) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(alert.Title)),
		strings.ToLower(strings.TrimSpace(alert.Source)),
	}
	if alert.RuleID != nil {
		parts = append(parts, strings.ToLower(strings.TrimSpace(*alert.RuleID)))
	}
	if alert.Asset != nil {
		parts = append(parts, strings.ToLower(strings.TrimSpace(*alert.Asset)))
	}
	if alert.SrcIP != nil {
		parts = append(parts, alert.SrcIP.String())
	}
	for _, tag := range alert.Tags {
		parts = append(parts, strings.ToLower(strings.TrimSpace(tag)))
	}
	raw := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

// IsDuplicate returns true if an identical fingerprint was seen within the time window.
// It also records/refreshes the timestamp for the fingerprint.
func (ce *CorrelationEngine) IsDuplicate(fingerprint string) bool {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	now := time.Now()
	// Clean up expired entries
	for fp, t := range ce.fingerprints {
		if now.Sub(t) > ce.window {
			delete(ce.fingerprints, fp)
		}
	}

	if lastSeen, exists := ce.fingerprints[fingerprint]; exists {
		if now.Sub(lastSeen) <= ce.window {
			ce.fingerprints[fingerprint] = now
			return true
		}
	}

	ce.fingerprints[fingerprint] = now
	return false
}
