package job

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Errors a caller can tell apart, because it has something different to do
// about each: two are a submission it assembled wrongly, and one is a job it
// asked about that the queue never had.
var (
	// ErrNoKind is a submission with nothing to say what it is. Without a kind
	// the history cannot report what a row was and the panel cannot choose an
	// icon, and a default would mean nothing in both places.
	ErrNoKind = errors.New("a job needs a kind")

	// ErrNoWork is a submission with nothing to run.
	ErrNoWork = errors.New("a job needs work to do")

	// ErrNotFound is a job this queue does not have.
	ErrNotFound = errors.New("no such job")
)

// Runner is the work a job does.
//
// One method, and it knows nothing about the queue that runs it. That is the
// direction the dependency has to point: query, dump and transfer will satisfy
// this, and none of them may be imported here — nor could they be, since the
// queue has to be testable with no database, no network and no subprocess.
//
// The context is how the work is told to stop. Honouring it is what makes a
// job cancellable, and work that ignores it is work that cannot be cancelled
// however loudly the window says otherwise.
type Runner interface {
	Run(ctx context.Context, report Reporter) error
}

// Clock is where this package reads the time.
//
// Injected rather than taken from the package, because every duration derived
// here is a duration somebody reads off the screen, and a test about one that
// waits for real time to pass is a test that is slow when it passes and flaky
// when it does not.
type Clock func() time.Time

// Option configures a queue.
type Option func(*Queue)

// WithClock gives the queue somewhere other than the wall to read the time.
func WithClock(clock Clock) Option {
	return func(q *Queue) { q.now = clock }
}

// Spec is what a job is, as opposed to what it does.
//
// Kind is what the panel and the history group by — "backup", "restore",
// "transfer". Title is the one line a person reads to tell this job from the
// one below it, so it names the thing operated on rather than the operation.
type Spec struct {
	Kind  string
	Title string
}

// View is a job as everything outside this package sees it: a copy, taken
// under the lock, that cannot change while it is being read.
//
// The error is a string rather than an error because this is what crosses to
// the window and goes into the history. What a caller does with a failed job
// is show it or store it, never unwrap it.
type View struct {
	ID       string
	Kind     string
	Title    string
	State    State
	Err      string
	Progress ProgressView
	// Started is when the work began. Ended is the zero time until it has
	// ended: a caller that renders the zero time shows year 1, which is loud
	// enough to catch, unlike an end quietly equal to the start.
	Started time.Time
	Ended   time.Time
}

// Queue holds every job the application has run since it started, and runs
// what is submitted to it.
//
// Each job gets a goroutine of its own. There is no limit on how many run at
// once because nothing yet asks for one: the limit that matters for a backup
// is the server's, not the window's, and inventing a number here would be
// inventing a policy nobody has needed.
type Queue struct {
	mu   sync.RWMutex
	jobs map[string]*record
	now  Clock
}

// record is a job as the queue holds it. Everything mutable about a job lives
// here, behind the queue's lock; done is closed once and only once, when the
// job reaches a state it cannot leave.
type record struct {
	view View
	// said is the last thing the work reported. Kept raw, so that what is
	// derived from it is derived at the moment it is read and not at the
	// moment it was said — which is what lets Elapsed grow while a job runs
	// without the work having to report anything for it to.
	said Progress
	done chan struct{}
}

// NewQueue creates an empty queue.
func NewQueue(options ...Option) *Queue {
	queue := &Queue{jobs: make(map[string]*record), now: time.Now}
	for _, option := range options {
		option(queue)
	}

	return queue
}

// Submit files the work and starts it, answering the identifier it is filed
// under.
//
// It does not wait: submitting is what the window does on a click, and a click
// that blocks until a backup finishes is the defect this whole package exists
// to prevent.
func (q *Queue) Submit(spec Spec, run Runner) (string, error) {
	if spec.Kind == "" {
		return "", ErrNoKind
	}

	if run == nil {
		return "", fmt.Errorf("%w: %s", ErrNoWork, spec.Kind)
	}

	id := newID()
	held := &record{
		view: View{ID: id, Kind: spec.Kind, Title: spec.Title, State: Pending},
		done: make(chan struct{}),
	}

	q.mu.Lock()
	q.jobs[id] = held
	q.mu.Unlock()

	go q.work(held, run)

	return id, nil
}

// work runs one job and records how it ended.
//
// The deferred function is the whole point of the goroutine's shape: it runs
// whether the work returned, returned an error or panicked, so there is no
// path out of here that leaves a job stuck in Running with nobody coming back
// for it.
func (q *Queue) work(held *record, run Runner) {
	var failure error

	defer func() {
		// A panic unwinds this goroutine and would take the process with it.
		// Caught here, it is a job that failed: the queue keeps running, the
		// window stays open, and what was panicked with is what the person is
		// told. Losing it would report a failure and nothing about why.
		if panicked := recover(); panicked != nil {
			failure = fmt.Errorf("the job panicked: %v", panicked)
		}

		q.finish(held, failure)
	}()

	q.moveTo(held, Running)

	failure = run.Run(context.Background(), reporterFor(q, held))
}

// reporter is the Reporter one job is handed. It holds the queue and the job
// it belongs to, so that work cannot report against a job it was not given.
type reporter struct {
	queue *Queue
	held  *record
}

func reporterFor(queue *Queue, held *record) Reporter {
	return reporter{queue: queue, held: held}
}

// Report keeps the latest word and discards the rest.
//
// Work that keeps talking after it returned — a goroutine of its own that
// nobody joined — must not rewrite the progress of a job somebody has already
// been told is over, so a job that has ended stops listening.
func (r reporter) Report(said Progress) {
	r.queue.mu.Lock()
	defer r.queue.mu.Unlock()

	if r.held.view.State.Over() {
		return
	}

	r.held.said = said
}

// finish puts a job into the end its outcome calls for and releases whoever is
// waiting on it.
func (q *Queue) finish(held *record, failure error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	end := Done
	if failure != nil {
		end = Failed
	}

	// The state machine is asked rather than assigned to. It is what refuses
	// an end for a job that already reached one — cancelled while it was still
	// waiting to start, whose work then ran and returned anyway. The end that
	// got there first is the one that counts, and done is already closed.
	moved, err := Transitioned(held.view.State, end)
	if err != nil {
		return
	}

	held.view.State = moved
	held.view.Ended = q.now()

	if failure != nil {
		held.view.Err = failure.Error()
	}

	close(held.done)
}

// moveTo applies a transition, ignoring one the job cannot make.
//
// Ignoring rather than reporting, because every caller here is the queue
// itself acting on a job it has just looked at: a refused move means the job
// moved underneath, and the move that got there first is the one that counts.
func (q *Queue) moveTo(held *record, to State) {
	q.mu.Lock()
	defer q.mu.Unlock()

	moved, err := Transitioned(held.view.State, to)
	if err != nil {
		return
	}

	held.view.State = moved
	if moved == Running {
		held.view.Started = q.now()
	}
}

// Get answers what the queue knows about one job.
func (q *Queue) Get(id string) (View, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	held, found := q.jobs[id]
	if !found {
		return View{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return q.viewOf(held), nil
}

// viewOf assembles what a job looks like from outside. The caller holds the
// lock; everything here only reads.
//
// Elapsed is worked out at the moment of reading rather than kept on the
// record, which is what lets it grow while a job runs without the work
// reporting anything, and stop the moment the job ends.
func (q *Queue) viewOf(held *record) View {
	view := held.view

	until := held.view.Ended
	if until.IsZero() {
		until = q.now()
	}

	var elapsed time.Duration
	if !held.view.Started.IsZero() {
		elapsed = until.Sub(held.view.Started)
	}

	view.Progress = viewOf(held.said, elapsed)

	return view
}

// List answers every job the queue holds.
//
// Unordered, because the order a person wants — newest first — is a question
// about when jobs happened, and the queue does not yet know what time it is.
// Whoever renders decides, and gets the times to decide with when the history
// arrives.
func (q *Queue) List() []View {
	q.mu.RLock()
	defer q.mu.RUnlock()

	views := make([]View, 0, len(q.jobs))
	for _, held := range q.jobs {
		views = append(views, q.viewOf(held))
	}

	return views
}

// Wait blocks until the job is over, or until the caller gives up.
//
// The caller's context is what bounds it, never the job's: a backup that never
// answers must not hold whoever asked about it for ever. A job that has
// already ended answers at once.
func (q *Queue) Wait(ctx context.Context, id string) (View, error) {
	q.mu.RLock()
	held, found := q.jobs[id]
	q.mu.RUnlock()

	if !found {
		return View{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	select {
	case <-held.done:
		return q.Get(id)
	case <-ctx.Done():
		return View{}, fmt.Errorf("waiting for job %s: %w", id, ctx.Err())
	}
}

// newID returns the identifier a job is filed under.
//
// The same shape as a connection's, and for the same reason: it goes into the
// history on disk and into the window, and somebody who opens either has to
// recognise it as an identifier rather than mistake it for something to edit.
func newID() string {
	var raw [16]byte
	// Documented to fill the slice entirely or panic, so there is no failure
	// here to handle.
	_, _ = rand.Read(raw[:])

	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}
