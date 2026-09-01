// Package jsonpath resolves a dot-separated field path (e.g. "rule.level",
// "host.name") against a decoded JSON tree. Kept as its own small package
// (rather than living in internal/ingest, where this logic originated for
// Field Mapping Templates) because internal/service also needs it for alert
// dedup grouping (see AlertService.computeGroupKey), and internal/service
// must not depend on internal/ingest.
package jsonpath

import (
	"strconv"
	"strings"
)

// Resolve walks root (the result of json.Unmarshal(body, &any)) by path's
// dot-separated segments (e.g. "rule.level", "data.user.name"). It returns
// false the moment a segment is missing, or the value at that point is
// neither an object nor -- for an all-digit segment -- an array.
//
// An all-digit segment indexes into an array: "detect.behaviors.0.tactic"
// reaches the first behaviour's tactic. This matters because array
// addressing is pervasive in security telemetry (CrowdStrike behaviors[],
// CloudTrail Records[], Wazuh rule.groups[]), and without it a Field
// Mapping Template rule naming such a path resolved to nothing at all --
// silently, since a rule that does not resolve is simply skipped and looks
// no different from a field the payload never carried.
//
// A digit segment against an object still reads it as a key first, so a
// payload with a literal "0" field keeps working; only when the current
// value is an array does the digit become an index. Negative and
// out-of-range indices return false rather than wrapping or panicking.
func Resolve(root any, path string) (any, bool) {
	current := root
	for _, segment := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			v, ok := node[segment]
			if !ok {
				return nil, false
			}
			current = v
		case []any:
			i, err := strconv.Atoi(segment)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			current = node[i]
		default:
			return nil, false
		}
	}
	return current, true
}
