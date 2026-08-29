package safego_test

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/safego"
)

// waitForGoroutine blocks (bounded) until fn signals completion via a
// channel it closes -- goroutines are inherently async, so every test
// below needs this instead of asserting on state immediately after Go
// returns.
func waitForGoroutine(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not complete in time")
	}
}

func TestGo_RunsFnOnItsOwnGoroutine(t *testing.T) {
	done := make(chan struct{})
	var ran bool
	safego.Go("test", func() {
		ran = true
		close(done)
	})
	waitForGoroutine(t, done)
	assert.True(t, ran)
}

func TestGo_RecoversAPanicInsteadOfCrashingTheProcess(t *testing.T) {
	done := make(chan struct{})
	safego.Go("test-panic", func() {
		defer close(done)
		panic("boom")
	})
	// The mere fact that this line is reached at all (the test process is
	// still alive to run it) is most of the assertion -- an unrecovered
	// panic on the goroutine above would have taken the whole test binary
	// down instead.
	waitForGoroutine(t, done)
}

func TestGo_LogsThePanicWithTheGivenName(t *testing.T) {
	// Use a synchronous handler wrapper so the write to buf happens
	// on-goroutine, before safego.Go's own recover() returns -- panic
	// recovery happens in a defer that runs AFTER fn's own defers (e.g. one
	// closing a "done" channel from inside fn), so a done-channel-based
	// wait here would race the log write itself. Polling the buffer is the
	// robust way to wait for a log line whose write genuinely happens after
	// the signal fn itself could give.
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&syncWriter{mu: &mu, buf: &buf}, nil)))
	defer slog.SetDefault(prev)

	safego.Go("my-goroutine-name", func() {
		panic("something went wrong")
	})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return buf.Len() > 0
	}, 2*time.Second, 10*time.Millisecond, "expected a log line from the recovered panic")

	mu.Lock()
	logged := buf.String()
	mu.Unlock()
	assert.Contains(t, logged, "panic in background goroutine")
	assert.Contains(t, logged, "my-goroutine-name")
	assert.Contains(t, logged, "something went wrong")
}

// syncWriter serializes writes to buf -- slog.NewJSONHandler doesn't
// guarantee its own locking around an arbitrary io.Writer, and this test
// reads buf concurrently from the polling goroutine above.
type syncWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func TestGo_MultiplePanickingGoroutinesEachRecoverIndependently(t *testing.T) {
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		safego.Go("concurrent", func() {
			defer wg.Done()
			panic("boom")
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitForGoroutine(t, done)
}

func TestGo_APanicWithANonStringValueIsStillHandled(t *testing.T) {
	done := make(chan struct{})
	require.NotPanics(t, func() {
		safego.Go("struct-panic", func() {
			defer close(done)
			panic(struct{ Code int }{Code: 42})
		})
		waitForGoroutine(t, done)
	})
}
