// Package jsonpath resolves a dot-separated field path (e.g. "rule.level",
// "host.name") against a decoded JSON tree. Kept as its own small package
// (rather than living in internal/ingest, where this logic originated for
// Field Mapping Templates) because internal/service also needs it for alert
// dedup grouping (see AlertService.computeGroupKey), and internal/service
// must not depend on internal/ingest.
package jsonpath

import "strings"

// Resolve walks root (the result of json.Unmarshal(body, &any)) by path's
// dot-separated segments (e.g. "rule.level", "data.user.name"). It returns
// false the moment any segment is missing or the value at that point isn't
// an object -- there's no array-index support, only plain nested-object
// field access.
func Resolve(root any, path string) (any, bool) {
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
