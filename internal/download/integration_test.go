package download

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The fake-based tests cover the queue's own logic. This one runs the real
// path — New(), stream.Server, actual HTTP, actual files on disk — so a break
// in the wiring between the two packages cannot pass unnoticed.
func TestQueueDownloadsRealFilesOneConnectionAtATime(t *testing.T) {
	const body = "the quick brown fox jumps over the lazy dog"

	var (
		inFlight atomic.Int32
		peak     atomic.Int32
		hits     atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		hits.Add(1)

		// Hold the connection open briefly so an overlapping request would be
		// caught rather than slipping past.
		time.Sleep(150 * time.Millisecond)
		w.Write([]byte(body + r.URL.Path))
		inFlight.Add(-1)
	}))
	defer srv.Close()

	dir := t.TempDir()

	var mu sync.Mutex
	changes := 0
	q := New(func() {
		mu.Lock()
		changes++
		mu.Unlock()
	})
	defer q.StopAll()

	names := []string{"one.mkv", "two.mkv", "three.mkv"}
	ids := make([]int, 0, len(names))
	for _, name := range names {
		ids = append(ids, q.Add(name, srv.URL+"/"+name, filepath.Join(dir, name)))
	}

	// Wait for the whole queue to drain.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("queue did not finish; jobs = %+v", q.Jobs())
		}
		allDone := true
		for _, s := range q.Jobs() {
			if !s.State.Done() {
				allDone = false
			}
		}
		if allDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := peak.Load(); got != 1 {
		t.Errorf("peak concurrent upstream connections = %d, want exactly 1", got)
	}
	if got := hits.Load(); got != int32(len(names)) {
		t.Errorf("upstream was hit %d times, want %d (one per file)", got, len(names))
	}

	for i, name := range names {
		if got := stateOf(q, ids[i]); got != StateDone {
			t.Errorf("%s state = %v, want Done", name, got)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if want := body + "/" + name; string(data) != want {
			t.Errorf("%s contents = %q, want %q", name, data, want)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if changes == 0 {
		t.Error("onChange never fired; the UI would never refresh")
	}
}

// Hold must stop the queue from opening the next connection, which is what lets
// playback own the single connection while it streams.
func TestHoldBlocksTheNextRealDownload(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	q := New(nil)
	defer q.StopAll()

	q.Hold()
	q.Add("held", srv.URL+"/held", filepath.Join(dir, "held.mkv"))

	time.Sleep(300 * time.Millisecond)
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream was contacted %d times while held, want 0", got)
	}

	q.Release()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("download never started after Release")
}
