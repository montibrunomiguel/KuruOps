package httpserver

import (
	"net/http"
	"time"
)

// Timeouts are the net/http server deadlines one binary runs with. Every cmd/*
// used to set only ReadHeaderTimeout, which bounds how long a client may take
// to send the request *line and headers* and nothing else: a client that
// trickled a body, never read a response, or parked an idle keep-alive
// connection held a goroutine and a socket for as long as it liked.
type Timeouts struct {
	// ReadHeader bounds the request line and headers (slowloris).
	ReadHeader time.Duration
	// Read bounds the whole request including its body.
	Read time.Duration
	// Write bounds producing the response, measured from the end of the request
	// headers. A route that legitimately needs longer (the SSE stream, the
	// external-database migration) extends its own deadline per request with
	// http.ResponseController -- see middleware.ExtendWriteDeadline -- rather
	// than every route inheriting a generous value.
	Write time.Duration
	// Idle bounds a keep-alive connection waiting for its next request.
	Idle time.Duration
}

// IngestTimeouts suit cmd/ingest, the only internet-facing process: a webhook
// body is capped at 1 MiB (see ingest.maxBodyBytes) and answered in
// milliseconds, so anything slower than 30s is not a SIEM forwarder, it is
// something holding a connection open.
var IngestTimeouts = Timeouts{
	ReadHeader: 5 * time.Second,
	Read:       30 * time.Second,
	Write:      30 * time.Second,
	Idle:       90 * time.Second,
}

// InternalTimeouts suit a process whose only HTTP surface is /healthz, /livez
// and /metrics for the orchestrator and Prometheus (cmd/worker).
var InternalTimeouts = Timeouts{
	ReadHeader: 5 * time.Second,
	Read:       10 * time.Second,
	Write:      10 * time.Second,
	Idle:       60 * time.Second,
}

// NewServer builds an *http.Server with t applied. Centralised so the three
// binaries cannot drift apart again (they did: each set its own subset).
func NewServer(addr string, handler http.Handler, t Timeouts) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
	}
}
