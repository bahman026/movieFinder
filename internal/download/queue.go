// Package download runs saved-file downloads one at a time.
//
// The whole point is the ordering guarantee: no matter how many titles are
// added, exactly one upstream connection is ever open. That is the same
// constraint internal/stream is built around — the app is expected to cost the
// network one connection — so a queue that ran jobs in parallel would quietly
// undo it. Jobs therefore wait their turn, and each one finishes sooner than it
// would if bandwidth were split between all of them.
//
// A Queue owns a single worker goroutine. Everything the UI touches goes
// through the mutex and is handed back as a Snapshot, so the Fyne thread never
// reads a job the worker is mutating.
package download

import (
	"context"
	"os"
	"sync"
	"time"

	"moviefinder/internal/safe"
	"moviefinder/internal/stream"
)

// State is where a job is in its life.
type State int

const (
	StateQueued State = iota
	StateDownloading
	StatePaused
	StateDone
	StateFailed
	StateCanceled
)

func (s State) String() string {
	switch s {
	case StateQueued:
		return "Queued"
	case StateDownloading:
		return "Downloading"
	case StatePaused:
		return "Paused"
	case StateDone:
		return "Done"
	case StateFailed:
		return "Failed"
	case StateCanceled:
		return "Canceled"
	}
	return "Unknown"
}

// Done reports whether the job has reached a state it will not leave on its own.
func (s State) Done() bool {
	return s == StateDone || s == StateFailed || s == StateCanceled
}

// job is the queue's own mutable record. The UI only ever sees a Snapshot.
type job struct {
	id       int
	title    string
	url      string
	savePath string

	state      State
	downloaded int64
	total      int64
	err        error

	srv downloader
	// userPaused separates "the user pressed Pause" from "the queue paused this
	// because playback needed the connection". Release must resume only the
	// latter, or it would restart a job the user deliberately stopped.
	userPaused bool
	autoPaused bool
}

// Snapshot is an immutable view of one job, safe to read on the UI thread.
type Snapshot struct {
	ID         int
	Title      string
	SavePath   string
	State      State
	Downloaded int64
	Total      int64 // -1 when the host does not report a length
	Err        error
}

// Queue runs downloads sequentially.
type Queue struct {
	mu     sync.Mutex
	jobs   []*job
	nextID int
	active *job
	held   bool // playback has the connection; do not start anything new

	wake     chan struct{}
	onChange func()
	// newServer is swapped in tests so the queue can be exercised without a
	// real network transfer.
	newServer func(url, savePath string, resumeFrom int64) downloader
}

// downloader is the slice of stream.Server the queue depends on, named so tests
// can substitute a fake.
//
// There is deliberately no Pause here. A paused job in this queue gives its
// connection back rather than holding it open — that is what lets the next
// download start — so pausing is Stop, and resuming is a fresh downloader that
// picks up where the file on disk leaves off.
type downloader interface {
	StartDownload(ctx context.Context) error
	Progress() (downloaded, total int64, done bool, err error)
	Stop()
}

// New starts a queue. onChange fires whenever anything a Snapshot would show
// has changed; it runs on the worker goroutine, so a UI caller must marshal
// back to the UI thread itself (fyne.Do).
func New(onChange func()) *Queue {
	q := &Queue{
		wake:     make(chan struct{}, 1),
		onChange: onChange,
		newServer: func(url, savePath string, resumeFrom int64) downloader {
			return stream.NewResuming(url, savePath, resumeFrom)
		},
	}
	go q.run()
	return q
}

// Add puts a download at the back of the queue and returns its id.
func (q *Queue) Add(title, url, savePath string) int {
	q.mu.Lock()
	q.nextID++
	id := q.nextID
	q.jobs = append(q.jobs, &job{
		id: id, title: title, url: url, savePath: savePath,
		state: StateQueued, total: -1,
	})
	q.mu.Unlock()

	q.notify()
	q.poke()
	return id
}

// Jobs returns the queue in order, oldest first.
func (q *Queue) Jobs() []Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]Snapshot, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, Snapshot{
			ID: j.id, Title: j.title, SavePath: j.savePath, State: j.state,
			Downloaded: j.downloaded, Total: j.total, Err: j.err,
		})
	}
	return out
}

// Counts summarises the queue for a status line.
func (q *Queue) Counts() (active, queued, done int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		switch {
		case j.state == StateDownloading || j.state == StatePaused:
			active++
		case j.state == StateQueued:
			queued++
		case j.state.Done():
			done++
		}
	}
	return active, queued, done
}

// Pause suspends a job and hands the connection to whatever is waiting behind
// it. A job that has not started yet can be paused too: it then simply keeps
// its place in the list without being eligible to run.
//
// Pausing the running job STOPS its transfer rather than holding the
// connection open. Holding it would mean two open connections as soon as the
// next job started, which is the one thing this package exists to prevent.
// What was already written stays on disk, and Resume continues from there.
func (q *Queue) Pause(id int) {
	q.mu.Lock()
	j := q.find(id)
	if j == nil || (j.state != StateDownloading && j.state != StateQueued) {
		q.mu.Unlock()
		return
	}
	j.userPaused = true
	j.state = StatePaused

	srv := j.srv
	j.srv = nil
	freed := q.active == j
	if freed {
		q.active = nil
	}
	q.mu.Unlock()

	if srv != nil {
		srv.Stop()
	}
	q.notify()
	if freed {
		q.poke() // let the next job have the connection
	}
}

// Resume puts a paused job back in line. It starts when the worker reaches it
// — immediately if nothing else is running — and continues from the bytes
// already on disk rather than starting the file again.
func (q *Queue) Resume(id int) {
	q.mu.Lock()
	j := q.find(id)
	if j == nil || j.state != StatePaused {
		q.mu.Unlock()
		return
	}
	j.userPaused = false
	// Still held for playback: leave it paused, but stop calling it the user's
	// doing so Release picks it up.
	if q.held {
		j.autoPaused = true
		q.mu.Unlock()
		q.notify()
		return
	}
	j.autoPaused = false
	j.state = StateQueued
	q.mu.Unlock()

	q.notify()
	q.poke()
}

// MoveUp moves a job one place towards the front of the queue, and MoveDown
// one place towards the back. The list order is the order the worker takes
// jobs in, so this is how the next download is chosen. Neither interrupts
// whatever is already running — reordering decides what goes next, and pausing
// the running job is what makes "next" happen now.
func (q *Queue) MoveUp(id int) bool { return q.move(id, -1) }

// MoveDown moves a job one place towards the back of the queue.
func (q *Queue) MoveDown(id int) bool { return q.move(id, +1) }

func (q *Queue) move(id, delta int) bool {
	q.mu.Lock()
	moved := false
	for i, j := range q.jobs {
		if j.id != id {
			continue
		}
		if swap := i + delta; swap >= 0 && swap < len(q.jobs) {
			q.jobs[i], q.jobs[swap] = q.jobs[swap], q.jobs[i]
			moved = true
		}
		break // found it, whether or not it could move
	}
	q.mu.Unlock()

	if moved {
		q.notify()
	}
	return moved
}

// Cancel stops a job, whether it is running or still waiting. The partial file
// is left on disk, matching what cancelling a play-along download does.
func (q *Queue) Cancel(id int) {
	q.mu.Lock()
	j := q.find(id)
	if j == nil || j.state.Done() {
		q.mu.Unlock()
		return
	}
	j.state = StateCanceled
	srv := j.srv
	if q.active == j {
		q.active = nil
	}
	q.mu.Unlock()

	if srv != nil {
		srv.Stop()
	}
	q.notify()
	q.poke()
}

// Remove drops a finished job from the list. A running job is cancelled first.
func (q *Queue) Remove(id int) {
	q.Cancel(id)

	q.mu.Lock()
	for i, j := range q.jobs {
		if j.id == id {
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
			break
		}
	}
	q.mu.Unlock()
	q.notify()
}

// ClearFinished removes every job that has stopped, leaving the live ones.
func (q *Queue) ClearFinished() {
	q.mu.Lock()
	kept := q.jobs[:0]
	for _, j := range q.jobs {
		if !j.state.Done() {
			kept = append(kept, j)
		}
	}
	q.jobs = kept
	q.mu.Unlock()
	q.notify()
}

// Hold pauses the queue because something else needs the one connection —
// playback with "download while playing" ticked. A running job is suspended
// rather than cancelled, so it resumes from where it stopped.
func (q *Queue) Hold() {
	q.mu.Lock()
	if q.held {
		q.mu.Unlock()
		return
	}
	q.held = true
	var srv downloader
	if j := q.active; j != nil && j.state == StateDownloading {
		j.autoPaused = true
		j.state = StatePaused
		srv = j.srv
		j.srv = nil
		q.active = nil
	}
	q.mu.Unlock()

	// Stopped, not merely paused: playback is about to open its own connection,
	// and a suspended-but-open one beside it would be the second.
	if srv != nil {
		srv.Stop()
	}
	q.notify()
}

// Release gives the connection back to the queue and re-queues whatever Hold
// suspended, which continues from the bytes already saved. A job the user
// paused by hand stays paused.
func (q *Queue) Release() {
	q.mu.Lock()
	if !q.held {
		q.mu.Unlock()
		return
	}
	q.held = false
	for _, j := range q.jobs {
		if !j.autoPaused {
			continue
		}
		j.autoPaused = false
		if !j.userPaused && j.state == StatePaused {
			j.state = StateQueued
		}
	}
	q.mu.Unlock()

	q.notify()
	q.poke()
}

// Held reports whether the queue is currently yielding to playback.
func (q *Queue) Held() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.held
}

// StopAll cancels everything. Called when the app closes so no transfer is left
// running behind it.
func (q *Queue) StopAll() {
	q.mu.Lock()
	var servers []downloader
	for _, j := range q.jobs {
		if !j.state.Done() {
			j.state = StateCanceled
			if j.srv != nil {
				servers = append(servers, j.srv)
			}
		}
	}
	q.active = nil
	q.mu.Unlock()

	for _, s := range servers {
		s.Stop()
	}
	q.notify()
}

// run is the single worker. It takes one job at a time and does not look at the
// next until the current one has stopped — this loop is the sequential
// guarantee the package exists for.
func (q *Queue) run() {
	for {
		j := q.takeNext()
		if j == nil {
			<-q.wake
			continue
		}
		// A panic mid-transfer must not kill this worker: unguarded it would
		// take the whole process down, and even surviving it would strand
		// every queued download behind a worker that is no longer running.
		// The job is marked failed and the queue moves on to the next one.
		safe.Run(func() { q.runJob(j) }, func(err error) { q.failJob(j, err) })
	}
}

// failJob records why a job stopped and releases the worker for the next one.
// A cancelled job keeps its own state — the user's decision outranks the
// failure that cancelling caused.
func (q *Queue) failJob(j *job, err error) {
	q.mu.Lock()
	if j.state != StateCanceled {
		j.state, j.err = StateFailed, err
	}
	q.mu.Unlock()

	q.clearActive(j)
	q.notify()
}

// takeNext claims the oldest waiting job, or nil when there is nothing to do
// (queue empty, held for playback, or one already running).
func (q *Queue) takeNext() *job {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.held || q.active != nil {
		return nil
	}
	for _, j := range q.jobs {
		if j.state == StateQueued {
			j.state = StateDownloading
			q.active = j
			return j
		}
	}
	return nil
}

// runJob downloads one file and returns only once it has finished, failed or
// been cancelled.
func (q *Queue) runJob(j *job) {
	// The file on disk is the authority on how much was already fetched: a
	// previous attempt may have written more than the last progress tick saw.
	srv := q.newServer(j.url, j.savePath, partialSize(j.savePath))

	q.mu.Lock()
	j.srv = srv
	canceled := j.state == StateCanceled
	q.mu.Unlock()
	if canceled { // cancelled in the gap before the transfer began
		q.clearActive(j)
		return
	}
	q.notify()

	if err := srv.StartDownload(context.Background()); err != nil {
		q.failJob(j, err)
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		downloaded, total, done, derr := srv.Progress()

		q.mu.Lock()
		if j.state == StateCanceled {
			q.mu.Unlock()
			q.clearActive(j)
			return
		}
		// Pause and Hold stop the transfer and clear j.srv, having already
		// handed the slot on. Detaching is what ends this run — not the paused
		// state, which Resume may have flipped back before this tick even
		// arrived. The error that stopping caused is not a failure, and the
		// byte count is kept because it is where the next attempt starts.
		if j.srv != srv {
			j.downloaded = downloaded
			q.mu.Unlock()
			q.notify()
			return
		}
		j.downloaded, j.total = downloaded, total
		switch {
		case derr != nil:
			j.state, j.err = StateFailed, derr
		case done:
			j.state = StateDone
		}
		finished := j.state.Done()
		q.mu.Unlock()

		q.notify()
		if finished {
			q.clearActive(j)
			return
		}
	}
}

// clearActive releases the worker slot and lets the next job start.
func (q *Queue) clearActive(j *job) {
	q.mu.Lock()
	if q.active == j {
		q.active = nil
	}
	q.mu.Unlock()
	q.poke()
}

// partialSize is how much of a save file is already on disk, and so where a
// resumed download continues from. A missing file means starting over.
func partialSize(savePath string) int64 {
	info, err := os.Stat(savePath)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func (q *Queue) find(id int) *job {
	for _, j := range q.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

// poke wakes the worker without blocking if it is already awake.
func (q *Queue) poke() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) notify() {
	if q.onChange != nil {
		q.onChange()
	}
}
