package ingest

import (
	"encoding/json"
	"strings"

	"github.com/argusops/argusops/internal/domain"
)

// resolveJSONPath walks a decoded JSON tree (the result of
// json.Unmarshal(body, &any)) by a dot-separated path (e.g. "rule.level",
// "data.user.name"). It returns false the moment any segment is missing or
// the value at that point isn't an object -- there's no array-index
// support, matching the simple scalar-field extraction
// domain.FieldMappingRule.JSONPath's doc comment describes.
func resolveJSONPath(root any, path string) (any, bool) {
	current := root
	for _, segment := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := obj[segment]
		if !ok {
			return nil, false
		}
		current = v
	}
	return current, true
}

// applyFieldMappingTemplate layers a webhook endpoint's configured rules on
// top of the alert's already-extracted metadata (see extractMetadata) --
// each rule pulls one field out of the full raw body (not just the
// sender's own "metadata" object) and adds it under Label, but only when
// that label isn't already present: the sender's own metadata always wins
// on conflict (see domain.FieldMappingTemplate's doc comment for why). A
// rule whose path doesn't exist on this particular alert is skipped
// silently -- not every alert from one endpoint necessarily carries every
// field a template expects.
func applyFieldMappingTemplate(body []byte, rules []domain.FieldMappingRule, metadata json.RawMessage) json.RawMessage {
	if len(rules) == 0 {
		return metadata
	}

	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return metadata
	}

	merged := map[string]json.RawMessage{}
	if len(metadata) > 0 {
		// extractMetadata already guarantees this unmarshals into an
		// object -- ignoring the error here would only matter if that
		// guarantee broke, in which case falling back to an empty map (and
		// so keeping every rule) is the safer failure mode.
		_ = json.Unmarshal(metadata, &merged)
	}

	changed := false
	for _, rule := range rules {
		if _, exists := merged[rule.Label]; exists {
			continue
		}
		value, ok := resolveJSONPath(root, rule.JSONPath)
		if !ok {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			continue
		}
		merged[rule.Label] = raw
		changed = true
	}
	if !changed {
		return metadata
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return metadata
	}
	return out
}
