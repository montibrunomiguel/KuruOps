// Package httpguard builds an http.Client that refuses to connect to
// loopback, link-local, or private-network addresses -- for dialing any URL
// an admin/tenant configured through this app's UI (a webhook destination,
// an MCP server endpoint, a self-hosted/OpenAI-compatible LLM base URL),
// where the value comes from someone with Settings access, not from this
// codebase's own trusted config. Without this, that access is enough to
// make kuruops-api/kuruops-worker issue a request to
// http://169.254.169.254/... (a cloud metadata endpoint) or
// http://localhost:5432/... (an internal service that trusts requests
// originating from this process) -- a classic SSRF pivot.
package httpguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// NewClient returns an http.Client whose Transport validates the actual IP
// address being connected to, not just the URL's hostname string -- a
// hostname-only check (e.g. rejecting the literal string "localhost") is
// trivially bypassed by DNS rebinding (a name that resolves to a public IP
// when a config is saved but a private one when the request is actually
// made) or by simply entering a raw private IP directly. Resolving inside
// DialContext and checking every resolved IP before connecting closes both
// gaps; the resolved IP is then dialed directly so a second, unguarded
// lookup inside the stdlib dialer can't return a different answer.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("httpguard: %w", err)
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("httpguard: resolve %s: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("httpguard: %s did not resolve to any address", host)
			}

			// Read fresh on every dial, not cached at client-construction
			// time -- this is what lets a test flip the env var with
			// t.Setenv around a single call, and costs nothing meaningful
			// in production where it's effectively constant for the life
			// of the process.
			if os.Getenv("ALLOW_PRIVATE_NETWORK_TARGETS") != "true" {
				for _, ip := range ips {
					if isDisallowed(ip.IP) {
						return nil, fmt.Errorf(
							"httpguard: refusing to connect to %s (%s) -- loopback/link-local/private addresses are blocked to prevent SSRF; set ALLOW_PRIVATE_NETWORK_TARGETS=true if this is a genuine on-prem deployment",
							host, ip.IP,
						)
					}
				}
			}

			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func isDisallowed(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified()
}
