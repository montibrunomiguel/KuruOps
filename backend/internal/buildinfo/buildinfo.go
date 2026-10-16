// Package buildinfo identifies the build a running binary came from.
//
// api, ingest and worker ship as three separate images built from one source
// tree. Rebuilding and restarting only one of them (`docker compose up --build
// api`) leaves the others running older code, and since they share a database
// and an event channel that skew shows up as behaviour that makes no sense for
// the code on disk: a queue consumer that predates the column it reads, an
// event payload one side cannot parse. Nothing fails loudly, so each binary
// states what it was built from -- in its startup log and as the
// kuruops_build_info metric -- and Prometheus alerts when more than one build
// is live at once (deploy/k8s/09-monitoring.yaml).
package buildinfo

// Build is a short hash of the sources the binary was compiled from. The
// backend Dockerfile computes it once, before compiling any of the three
// binaries, and injects it with -ldflags, so all three images built from the
// same tree agree. "dev" means a plain `go build` / `go run`.
var Build = "dev"
