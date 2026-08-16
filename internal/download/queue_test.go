package download

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeDownloader stands in for stream.Server so the queue can be tested without
// a network transfer. It records how many are running at once, which is what
// the sequential guarantee is about.
type fakeDownloader struct {
	tracker *tracker
	name    string

	mu       sync.Mutex
	started  bool
	paused   bool
	stopped  bool
	done     bool
	ended    bool // connection closed; guards a double decrement
	err      error
	progress int64
	startErr error
}

type tracker struct {
	mu      sync.Mutex
	running int
	maxSeen int
	order   []string
}

func (t *tracker) begin(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running++
	if t.running > t.maxSeen {
		t.maxSeen = t.running
	}
	t.order = append(t.order, name)
}

func (t *tracker) end() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running--
}

func (t *tracker) peak() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxSeen
}

func (t *tracker) sequence() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.order...)
}

// StartDownload is where the upstream connection opens, so that is where the
// tracker counts one as in flight — and it closes again in complete/fail/Stop.
// Counting at construction instead would overlap the finished job with the next
// one the queue immediately starts, and report a concurrency that never existed.
func (f *fakeDownloader) StartDownload(context.Context) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	f.tracker.begin(f.name)
	return nil
}

// endOnce closes the tracked connection exactly once.
func (f *fakeDownloader) endOnce() {
	f.mu.Lock()
	if f.ended || !f.started {
		f.mu.Unlock()
		return
	}
	f.ended = true
	f.mu.Unlock()
	f.tracker.end()
}

func (f *fakeDownloader) Progress() (int64, int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progress, 100, f.done, f.err
}

func (f *fakeDownloader) Pause()  { f.mu.Lock(); f.paused = true; f.mu.Unlock() }
func (f *fakeDownloader) Resume() { f.mu.Lock(); f.paused = false; f.mu.Unlock() }

func (f *fakeDownloader) Stop() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	f.endOnce()
}

func (f *fakeDownloader) complete() {
	f.mu.Lock()
	f.done, f.progress = true, 100
	f.mu.Unlock()
	f.endOnce()
}

func (f *fakeDownloader) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.endOnce()
}

func (f *fakeDownloader) isPaused() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused
}

func (f *fakeDownloader) wasStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// newTestQueue builds a queue whose downloads are fakes, and hands back a
// lookup so a test can drive each one.
func newTestQueue(t *testing.T) (*Queue, *tracker, func(string) *fakeDownloader) {
	return newTestQueueWith(t, nil)
}

// newTestQueueWith is newTestQueue with the option of substituting a different
// downloader for particular URLs — a misbehaving one, say. special returns nil
// for any URL it does not want to take over.
func newTestQueueWith(t *testing.T, special func(url string) downloader) (*Queue, *tracker, func(string) *fakeDownloader) {
	t.Helper()

	tr := &tracker{}
	var mu sync.Mutex
	fakes := map[string]*fakeDownloader{}

	q := &Queue{wake: make(chan struct{}, 1)}
	q.newServer = func(url, savePath string) downloader {
		if special != nil {
			if d := special(url); d != nil {
				return d
			}
		}
		mu.Lock()
		defer mu.Unlock()
		f := &fakeDownloader{tracker: tr, name: url}
		fakes[url] = f
		return f
	}
	go q.run()

	get := func(url string) *fakeDownloader {
		mu.Lock()
		defer mu.Unlock()
		return fakes[url]
	}
	return q, tr, get
}

// waitFor polls until cond holds, so tests do not depend on a fixed sleep.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func stateOf(q *Queue, id int) State {
	for _, s := range q.Jobs() {
		if s.ID == id {
			return s.State
		}
	}
	return StateCanceled
}

// panicDownloader stands in for a transfer that hits something the code never
// expected — a rotated host answering with a shape no parser handles.
type panicDownloader struct{}

func (panicDownloader) StartDownload(context.Context) error {
	panic("the file host answered with something unexpected")
}
func (panicDownloader) Progress() (int64, int64, bool, error) { return 0, 0, false, nil }
func (panicDownloader) Pause()                                {}
func (panicDownloader) Resume()                               {}
func (panicDownloader) Stop()                                 {}

// A panic in one transfer must not take the worker with it. Unguarded it would
// end the whole process — the window closing mid-download — and even surviving
// that, a dead worker would strand every job queued behind it.
func TestPanicInOneTransferFailsThatJobAndNotTheQueue(t *testing.T) {
	// safe.Run logs the recovered stack; keep it out of the test output.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	q, _, get := newTestQueueWith(t, func(url string) downloader {
		if url == "url-boom" {
			return panicDownloader{}
		}
		return nil
	})

	boom := q.Add("Boom", "url-boom", "/tmp/boom")
	next := q.Add("Fine", "url-fine", "/tmp/fine")

	waitFor(t, "the panicking job to be marked failed", func() bool {
		return stateOf(q, boom) == StateFailed
	})
	waitFor(t, "the queue to carry on to the next job", func() bool {
		return get("url-fine") != nil
	})

	get("url-fine").complete()
	waitFor(t, "the next job to finish normally", func() bool {
		return stateOf(q, next) == StateDone
	})

	// The failure is recorded, not swallowed.
	for _, s := range q.Jobs() {
		if s.ID == boom && s.Err == nil {
			t.Error("the failed job carries no error to show the user")
		}
	}
}

// The guarantee the package exists for: many jobs, never two connections.
func TestQueueRunsOneAtATime(t *testing.T) {
	q, tr, get := newTestQueue(t)

	ids := []int{
		q.Add("A", "url-a", "/tmp/a"),
		q.Add("B", "url-b", "/tmp/b"),
		q.Add("C", "url-c", "/tmp/c"),
	}

	// Let each finish in turn; only one may ever be in flight.
	for i, url := range []string{"url-a", "url-b", "url-c"} {
		waitFor(t, "job "+url+" to start", func() bool { return get(url) != nil })
		if got := tr.peak(); got > 1 {
			t.Fatalf("%d downloads ran at once, want at most 1", got)
		}
		get(url).complete()
		waitFor(t, "job to report done", func() bool { return stateOf(q, ids[i]) == StateDone })
	}

	if got := tr.peak(); got != 1 {
		t.Errorf("peak concurrent downloads = %d, want 1", got)
	}
	if got := tr.sequence(); len(got) != 3 || got[0] != "url-a" || got[1] != "url-b" || got[2] != "url-c" {
		t.Errorf("ran in order %v, want a, b, c", got)
	}
}

// A job cancelled while still waiting must never open a connection.
func TestCancelingQueuedJobNeverStartsIt(t *testing.T) {
	q, _, get := newTestQueue(t)

	first := q.Add("A", "url-a", "/tmp/a")
	second := q.Add("B", "url-b", "/tmp/b")

	waitFor(t, "first job to start", func() bool { return get("url-a") != nil })

	q.Cancel(second)
	if got := stateOf(q, second); got != StateCanceled {
		t.Fatalf("queued job state = %v, want Canceled", got)
	}

	get("url-a").complete()
	waitFor(t, "first to finish", func() bool { return stateOf(q, first) == StateDone })

	// Give the worker a chance to wrongly pick up the cancelled job.
	time.Sleep(100 * time.Millisecond)
	if get("url-b") != nil {
		t.Error("cancelled job opened a connection anyway")
	}
}

func TestPauseAndResumeRunningJob(t *testing.T) {
	q, _, get := newTestQueue(t)
	id := q.Add("A", "url-a", "/tmp/a")
	waitFor(t, "job to start", func() bool { return get("url-a") != nil })

	q.Pause(id)
	waitFor(t, "job to pause", func() bool { return stateOf(q, id) == StatePaused })
	if !get("url-a").isPaused() {
		t.Error("pause did not reach the downloader")
	}

	q.Resume(id)
	waitFor(t, "job to resume", func() bool { return stateOf(q, id) == StateDownloading })
	if get("url-a").isPaused() {
		t.Error("resume did not reach the downloader")
	}
}

// Playback owns the one connection: Hold suspends the running job, Release
// puts it back exactly as it was.
func TestHoldSuspendsForPlaybackAndReleaseResumes(t *testing.T) {
	q, _, get := newTestQueue(t)
	id := q.Add("A", "url-a", "/tmp/a")
	waitFor(t, "job to start", func() bool { return get("url-a") != nil })

	q.Hold()
	waitFor(t, "job to be held", func() bool { return stateOf(q, id) == StatePaused })
	if !get("url-a").isPaused() {
		t.Error("hold did not pause the transfer")
	}

	// Nothing new may start while playback holds the connection.
	q.Add("B", "url-b", "/tmp/b")
	time.Sleep(100 * time.Millisecond)
	if get("url-b") != nil {
		t.Error("a new job started while the queue was held")
	}

	q.Release()
	waitFor(t, "job to resume", func() bool { return stateOf(q, id) == StateDownloading })
	if get("url-a").isPaused() {
		t.Error("release did not resume the transfer")
	}
}

// Release must not restart what the user paused on purpose.
func TestReleaseLeavesUserPausedJobPaused(t *testing.T) {
	q, _, get := newTestQueue(t)
	id := q.Add("A", "url-a", "/tmp/a")
	waitFor(t, "job to start", func() bool { return get("url-a") != nil })

	q.Pause(id)
	waitFor(t, "user pause", func() bool { return stateOf(q, id) == StatePaused })

	q.Hold()
	q.Release()

	time.Sleep(100 * time.Millisecond)
	if got := stateOf(q, id); got != StatePaused {
		t.Errorf("state after release = %v, want it to stay Paused", got)
	}
	if !get("url-a").isPaused() {
		t.Error("release resumed a job the user had paused")
	}
}

func TestFailedDownloadIsReportedAndQueueContinues(t *testing.T) {
	q, _, get := newTestQueue(t)

	failing := q.Add("A", "url-a", "/tmp/a")
	next := q.Add("B", "url-b", "/tmp/b")

	waitFor(t, "first to start", func() bool { return get("url-a") != nil })
	get("url-a").fail(errors.New("connection reset"))

	waitFor(t, "failure to surface", func() bool { return stateOf(q, failing) == StateFailed })

	// A failure must not wedge the queue.
	waitFor(t, "next job to start", func() bool { return get("url-b") != nil })
	get("url-b").complete()
	waitFor(t, "next to finish", func() bool { return stateOf(q, next) == StateDone })
}

func TestCancelRunningJobStopsTheTransfer(t *testing.T) {
	q, _, get := newTestQueue(t)
	id := q.Add("A", "url-a", "/tmp/a")
	waitFor(t, "job to start", func() bool { return get("url-a") != nil })

	q.Cancel(id)
	waitFor(t, "transfer to stop", func() bool { return get("url-a").wasStopped() })
	if got := stateOf(q, id); got != StateCanceled {
		t.Errorf("state = %v, want Canceled", got)
	}
}

func TestClearFinishedKeepsLiveJobs(t *testing.T) {
	q, _, get := newTestQueue(t)

	first := q.Add("A", "url-a", "/tmp/a")
	waitFor(t, "job to start", func() bool { return get("url-a") != nil })
	get("url-a").complete()
	waitFor(t, "job to finish", func() bool { return stateOf(q, first) == StateDone })

	live := q.Add("B", "url-b", "/tmp/b")
	q.ClearFinished()

	jobs := q.Jobs()
	if len(jobs) != 1 || jobs[0].ID != live {
		t.Fatalf("jobs after clear = %+v, want only the live one (%d)", jobs, live)
	}
}

func TestCountsSummarisesTheQueue(t *testing.T) {
	q, _, get := newTestQueue(t)

	done := q.Add("A", "url-a", "/tmp/a")
	q.Add("B", "url-b", "/tmp/b")
	q.Add("C", "url-c", "/tmp/c")

	waitFor(t, "job to start", func() bool { return get("url-a") != nil })
	get("url-a").complete()
	waitFor(t, "job to finish", func() bool { return stateOf(q, done) == StateDone })
	waitFor(t, "next to start", func() bool { return get("url-b") != nil })

	waitFor(t, "counts to settle", func() bool {
		active, queued, finished := q.Counts()
		return active == 1 && queued == 1 && finished == 1
	})
}
