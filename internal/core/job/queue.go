package job

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/gsoares85/hermes/internal/core/secret"
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

	// ErrStillRunning is a job asked to be forgotten while it is working.
	ErrStillRunning = errors.New("the job is still running")
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

// WithLogLines sets how many lines of a job's log are kept.
func WithLogLines(lines int) Option {
	return func(q *Queue) { q.logLines = lines }
}

// WithLogBytes sets how much of a job's log is kept.
func WithLogBytes(bytes int) Option {
	return func(q *Queue) { q.logBytes = bytes }
}

// WithCleanupTimeout sets how long a cancelled job's cleanup is given.
func WithCleanupTimeout(within time.Duration) Option {
	return func(q *Queue) { q.cleanupTimeout = within }
}

// How many jobs that have ended the queue goes on holding.
//
// It holds them for the window, which draws what happened in this session
// without going to disk for it. Past this many, the oldest are let go: they
// are in the history by then, the panel reads them from there, and what they
// cost here is a log apiece — a megabyte each, by the ceiling a log is kept
// under. A session of verbose restores would otherwise spend the whole memory
// budget of the application on work that finished hours ago.
const defaultFinishedKept = 100

// WithFinishedKept sets how many jobs that have ended the queue holds on to.
func WithFinishedKept(jobs int) Option {
	return func(q *Queue) { q.finishedKept = jobs }
}

// Observer is told whenever a job moves from one state to another.
//
// Only transitions, and never progress: a transition is rare and is what takes
// a bar off the screen, so it is worth telling somebody about the moment it
// happens. Progress is a million reports a second and is read by sampling
// instead.
//
// It is called with no lock held, but it is called from the goroutine of the
// job that moved. An observer that blocks blocks that job.
type Observer func(View)

// WithObserver gives the queue somebody to tell when a job changes state.
//
// Several of them are allowed, and each call adds one rather than replacing
// what was there: a job that ends is both a row the window redraws and a row
// the history keeps, and the two have nothing to do with each other. They are
// told in the order they were added.
func WithObserver(observe Observer) Option {
	return func(q *Queue) { q.observers = append(q.observers, observe) }
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
	// Dropped is how many lines the log's ceiling took. A log truncated in
	// silence is worse than a short one: somebody reads the first line kept as
	// the beginning of the operation.
	Dropped int
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
	mu             sync.RWMutex
	jobs           map[string]*record
	now            Clock
	logLines       int
	logBytes       int
	cleanupTimeout time.Duration
	finishedKept   int
	// ends counts the jobs that have reached an end, so that the queue can say
	// which of two ended first. The clock cannot: two jobs finishing inside
	// one tick of it carry the same instant, and on some systems a tick is
	// long enough for a great many jobs.
	ends      int
	observers []Observer
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
	log  *logbook
	// ended is where this job comes in the order the jobs finished, and is
	// zero until it has. It decides which one the queue lets go of first.
	ended int
	// stop tells the work to wind down. Kept per job rather than derived from
	// a context the queue holds, so that cancelling one job is exactly that.
	stop context.CancelFunc
	done chan struct{}
}

// NewQueue creates an empty queue.
func NewQueue(options ...Option) *Queue {
	queue := &Queue{
		jobs:           make(map[string]*record),
		now:            time.Now,
		logLines:       defaultLogLines,
		logBytes:       defaultLogBytes,
		cleanupTimeout: defaultCleanupTimeout,
		finishedKept:   defaultFinishedKept,
	}
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
	ctx, stop := context.WithCancel(context.Background())
	held := &record{
		// The title is free text from whoever submitted the job, and it is
		// drawn in the window and kept in the history — the same two places the
		// log goes, through the same door. A title assembled from a connection
		// string is the obvious way a password arrives here.
		view: View{ID: id, Kind: spec.Kind, Title: secret.Redact(spec.Title), State: Pending},
		log:  newLogbook(q.logLines, q.logBytes),
		stop: stop,
		done: make(chan struct{}),
	}

	q.mu.Lock()
	q.jobs[id] = held
	q.mu.Unlock()

	go q.work(ctx, held, run)

	return id, nil
}

// work runs one job and records how it ended.
//
// The deferred function is the whole point of the goroutine's shape: it runs
// whether the work returned, returned an error or panicked, so there is no
// path out of here that leaves a job stuck in Running with nobody coming back
// for it.
func (q *Queue) work(ctx context.Context, held *record, run Runner) {
	// Cancelled before it was picked up. The work must not run: the job is
	// already over, and starting it would begin a backup somebody called off.
	if !q.moveTo(held, Running) {
		held.stop()

		return
	}

	var failure error

	defer func() {
		// A panic unwinds this goroutine and would take the process with it.
		// Caught here, it is a job that failed: the queue keeps running, the
		// window stays open, and what was panicked with is what the person is
		// told. Losing it would report a failure and nothing about why.
		if panicked := recover(); panicked != nil {
			failure = fmt.Errorf("the job panicked: %v", panicked)
		}

		held.log.close()

		end, reported := q.endOf(held, run, failure)
		q.finish(held, end, reported)
		held.stop()
	}()

	failure = run.Run(ctx, reporterFor(q, held))
}

// endOf decides where a job that has stopped working belongs, and undoes what
// it started if it was cancelled.
//
// The cleanup runs here rather than beside the cancellation, so that it
// happens after the work has stopped: removing the partial file while the
// thing writing it is still writing races one against the other.
func (q *Queue) endOf(held *record, run Runner, failure error) (State, error) {
	if q.stateOf(held) != Cancelling {
		if failure != nil {
			return Failed, failure
		}

		return Done, nil
	}

	// Cancelling is a request, not a promise. Work that reached its end before
	// it noticed finished, and there is nothing partial to clean up after it:
	// reporting otherwise would call a backup that exists one that does not.
	if failure == nil {
		return Done, nil
	}

	// What the work returned is discarded from here on. Work that stops
	// because it was cancelled returns context.Canceled, and telling somebody
	// their backup failed because they pressed Stop is not a report, it is
	// noise dressed as one.
	if err := q.cleanup(run); err != nil {
		return Failed, err
	}

	return Cancelled, nil
}

// stateOf answers where a job is, for a caller that holds no lock.
func (q *Queue) stateOf(held *record) State {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return held.view.State
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

// Log is where this job's work writes what it is doing.
func (r reporter) Log() Log {
	return r.held.log
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

	// The step is what the work says it is doing, in its own words, and those
	// words are often a table or a server it was given. Same door as the title
	// and the log.
	said.Step = secret.Redact(said.Step)
	r.held.said = said
}

// finish puts a job into the end its outcome calls for and releases whoever is
// waiting on it.
func (q *Queue) finish(held *record, end State, failure error) {
	var announce *View
	defer func() { q.tell(announce) }()

	q.mu.Lock()
	defer q.mu.Unlock()

	announce = q.ended(held, end, failure)
}

// endIfWaiting ends a job the queue has not picked up yet, and leaves one it
// has alone.
//
// One taking of the lock rather than two, which is the whole point: a job read
// as waiting and ended a moment later is a job whose work started in between.
// The state machine would refuse that move, but only after the work had been
// let through — and the question here is whether it ever starts.
func (q *Queue) endIfWaiting(held *record) {
	var announce *View
	defer func() { q.tell(announce) }()

	q.mu.Lock()
	defer q.mu.Unlock()

	if held.view.State != Pending {
		return
	}

	announce = q.ended(held, Cancelled, nil)
}

// ended puts a job into the end its outcome calls for, answering what to
// announce or nil when the move was refused. The caller holds the lock.
func (q *Queue) ended(held *record, end State, failure error) *View {
	// The state machine is asked rather than assigned to. It is what refuses
	// an end for a job that already reached one — cancelled while it was still
	// waiting to start, whose work then ran and returned anyway. The end that
	// got there first is the one that counts, and done is already closed.
	moved, err := Transitioned(held.view.State, end)
	if err != nil {
		return nil
	}

	held.view.State = moved
	held.view.Ended = q.now()

	q.ends++
	held.ended = q.ends

	// Redacted here, where the log is redacted too, and for the same reason:
	// what a driver says when it cannot connect is the connection string it
	// was handed. This is the second piece of free text a job produces, it
	// reaches the same window and the same file on disk, and doing it on the
	// way out instead would leave the password in memory until then — and in
	// the history for ever.
	if failure != nil {
		held.view.Err = secret.Redact(failure.Error())
	}

	close(held.done)

	q.letGoOfTheOldest(held)

	announced := q.viewOf(held)

	return &announced
}

// letGoOfTheOldest drops finished jobs past the ceiling, oldest first.
//
// Only ones that have ended, and never the one that just did: a job still
// going that vanished from the queue would be work nothing on screen could
// stop, and a job dropped in the same breath as it ended would be one that
// whoever was waiting for it could no longer ask about. What is dropped has
// been written to the history by then, so the panel still shows it and reads
// its log from there.
//
// The caller holds the lock.
func (q *Queue) letGoOfTheOldest(justEnded *record) {
	finished := make([]*record, 0, len(q.jobs))
	for _, held := range q.jobs {
		if held.view.State.Over() {
			finished = append(finished, held)
		}
	}

	if len(finished) <= q.finishedKept {
		return
	}

	// In the order they ended, which is the order in which a person stops
	// looking at them. By the sequence rather than by the clock: jobs that
	// finish inside one tick share an instant, and then the oldest would be
	// whichever the map happened to hand over first.
	slices.SortFunc(finished, func(a, b *record) int { return a.ended - b.ended })

	for _, old := range finished[:len(finished)-q.finishedKept] {
		if old == justEnded {
			continue
		}

		delete(q.jobs, old.view.ID)
	}
}

// moveTo applies a transition, ignoring one the job cannot make.
//
// Ignoring rather than reporting, because every caller here is the queue
// itself acting on a job it has just looked at: a refused move means the job
// moved underneath, and the move that got there first is the one that counts.
func (q *Queue) moveTo(held *record, to State) bool {
	var announce *View
	defer func() { q.tell(announce) }()

	q.mu.Lock()
	defer q.mu.Unlock()

	moved, err := Transitioned(held.view.State, to)
	if err != nil {
		return false
	}

	held.view.State = moved
	if moved == Running {
		held.view.Started = q.now()
	}

	told := q.viewOf(held)
	announce = &told

	return true
}

// tell passes a transition on, once the lock is gone.
//
// After the lock rather than inside it: the observer is code this package did
// not write, and running it while holding the queue is how one slow window
// stops every job in it.
func (q *Queue) tell(announce *View) {
	if announce == nil {
		return
	}

	for _, observe := range q.observers {
		observe(*announce)
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
	view.Dropped = held.log.droppedCount()

	return view
}

// Log answers the lines a job's log holds, oldest first.
//
// A copy, so that what a caller is reading cannot change under it while a
// subprocess keeps writing.
func (q *Queue) Log(id string) ([]string, error) {
	q.mu.RLock()
	held, found := q.jobs[id]
	q.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return held.log.read(), nil
}

// LogSince answers what a job has said after a sequence, and the sequence to
// ask with next time.
//
// The reading a live view of a log needs, as opposed to the whole of it: a
// panel that has been shown the first hundred lines asks for what came after
// them, and gets nothing when nothing has. Sequences count what a job has said
// since it began, so they keep moving after the log reaches its ceiling and
// starts losing its beginning — which a count of the lines being held does not.
func (q *Queue) LogSince(id string, seq int) ([]string, int, error) {
	q.mu.RLock()
	held, found := q.jobs[id]
	q.mu.RUnlock()

	if !found {
		return nil, 0, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	fresh, next := held.log.since(seq)

	return fresh, next, nil
}

// Forget drops a job the queue no longer needs to remember.
//
// Only one that has ended. Forgetting a running job would take the row off the
// screen and leave the work going, with nothing able to stop it — which is a
// leak with a person watching the space where the Stop button was.
func (q *Queue) Forget(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	held, found := q.jobs[id]
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	if !held.view.State.Over() {
		return fmt.Errorf("%w: %s is %v", ErrStillRunning, id, held.view.State)
	}

	delete(q.jobs, id)

	return nil
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
