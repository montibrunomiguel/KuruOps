package handlers

import "time"

// SetSSETimingsForTest shortens the SSE keep-alive and per-write deadline so a
// test can watch a stream outlive a server WriteTimeout in well under a second.
// The returned func restores the production values.
func SetSSETimingsForTest(keepAlive, writeTimeout time.Duration) (restore func()) {
	prevKeepAlive, prevWrite := sseKeepAliveInterval, sseWriteTimeout
	sseKeepAliveInterval, sseWriteTimeout = keepAlive, writeTimeout
	return func() { sseKeepAliveInterval, sseWriteTimeout = prevKeepAlive, prevWrite }
}
