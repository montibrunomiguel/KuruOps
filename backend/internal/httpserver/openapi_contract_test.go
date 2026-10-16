package httpserver

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.yaml.in/yaml/v3"
)

// TestOpenAPIMatchesRouter keeps docs/openapi.yaml honest. The spec is
// hand-written, and a hand-written spec drifts: it was found missing 19
// endpoints at once (see the docs audit in CHANGELOG.md) because nothing tied it
// to the router. This walks the routes NewRouter really mounts and compares them,
// in both directions, with the operations the spec documents:
//
//   - a route the router serves but the spec omits is an undocumented endpoint;
//   - an operation the spec lists that the router does not serve is documentation
//     for something that does not exist (a renamed or removed route).
//
// It checks method + path only. Request and response schemas are not verified --
// that would need a generator -- but the failure this prevents (a whole endpoint
// nobody wrote down) is the expensive one.
func TestOpenAPIMatchesRouter(t *testing.T) {
	served := servedOperations(t)
	documented := documentedOperations(t)

	var undocumented, stale []string
	for op := range served {
		if _, ok := documented[op]; !ok {
			undocumented = append(undocumented, op)
		}
	}
	for op := range documented {
		if _, ok := served[op]; !ok {
			stale = append(stale, op)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(stale)

	if len(undocumented) > 0 {
		t.Errorf("routes served by the router but missing from docs/openapi.yaml (add them under `paths`):\n  %s",
			strings.Join(undocumented, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("operations in docs/openapi.yaml that the router does not serve (remove or fix them):\n  %s",
			strings.Join(stale, "\n  "))
	}
}

var pathParam = regexp.MustCompile(`\{[^}/]*\}`)

// normalizeOperation puts a router route and a spec path in one comparable form:
// parameter names vary ({id} vs {alertId}), chi keeps a trailing slash on a
// subrouter's root ("/api/v1/tags/") where the spec writes "/api/v1/tags", and a
// chi wildcard ("/images/*", which captures the rest of the path) is documented
// as a path parameter.
func normalizeOperation(method, path string) string {
	if strings.HasSuffix(path, "/*") {
		path = strings.TrimSuffix(path, "*") + "{}"
	}
	path = pathParam.ReplaceAllString(path, "{}")
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	return strings.ToUpper(method) + " " + path
}

func servedOperations(t *testing.T) map[string]struct{} {
	t.Helper()
	routes, ok := NewRouter(nilOptions()).(chi.Routes)
	if !ok {
		t.Fatalf("NewRouter's return value does not satisfy chi.Routes")
	}
	ops := map[string]struct{}{}
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// HEAD/OPTIONS are protocol plumbing, not API operations.
		if method == http.MethodHead || method == http.MethodOptions {
			return nil
		}
		ops[normalizeOperation(method, route)] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	return ops
}

func documentedOperations(t *testing.T) map[string]struct{} {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatalf("read docs/openapi.yaml: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse docs/openapi.yaml: %v", err)
	}
	// Keys of a path item that are not operations (shared parameters, summary...).
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true}
	ops := map[string]struct{}{}
	for path, item := range spec.Paths {
		for key := range item {
			if methods[key] {
				ops[normalizeOperation(key, path)] = struct{}{}
			}
		}
	}
	if len(ops) == 0 {
		t.Fatal("parsed no operations from docs/openapi.yaml -- the test would pass vacuously")
	}
	return ops
}
