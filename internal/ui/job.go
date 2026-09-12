package ui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
)

// How often the window is told where the running jobs are.
//
// The cap ADR-0015 put on emission, and the reason it is a cap rather than a
// delay: a COPY of a million rows reports per row, and every one of those
// crossing to the webview is the window not repainting. Sampling turns a
// million reports into ten a second, whatever the work does.
const defaultSampleEvery = 100 * time.Millisecond

// The events this service pushes. Named here rather than spelled out at each
// call, because the frontend subscribes to the same strings and a typo in one
// of them is a panel that silently never updates.
const (
	stateEvent    = "job:state"
	progressEvent = "job:progress"
	logEvent      = "job:log"
)

// Emitter is how the Go side pushes state to the window.
//
// An interface this service is handed rather than a global it reaches for.
// The gate already holds this package to that for the vault and the engine,
// and the reason is the same one again: a service that called
// application.Get() would need a running Wails application before it could be
// tested at all, and the cap above would be untestable with it.
type Emitter interface {
	Emit(name string, data any)
}

// JobProgressView is how far along a job is, as the window reads it.
//
// Durations cross as milliseconds. A Go duration marshals as a count of
// nanoseconds, which the window would have to know to divide by a billion —
// and dividing by the wrong power of ten is invisible until an ETA reads three
// hours for a job with three seconds left.
type JobProgressView struct {
	Step          string  `json:"step"`
	Unit          string  `json:"unit"`
	Done          int64   `json:"done"`
	Total         int64   `json:"total"`
	Fraction      float64 `json:"fraction"`
	Indeterminate bool    `json:"indeterminate"`
	ElapsedMs     int64   `json:"elapsedMs"`
	RemainingMs   int64   `json:"remainingMs"`
}

// JobView is a job as the window draws it.
//
// The state crosses as its name. A number would make the frontend carry a copy
// of an enumeration whose order is an implementation detail of a Go file, and
// reordering the constants would silently relabel every row.
//
// The times cross as strings, and an empty one means there is none. A job that
// has not ended has no end, and the zero time rendered by a date formatter is
// 1 January year 1 — which it will print rather than refuse.
type JobView struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	State     string          `json:"state"`
	Err       string          `json:"error"`
	Progress  JobProgressView `json:"progress"`
	Dropped   int             `json:"dropped"`
	StartedAt string          `json:"startedAt"`
	EndedAt   string          `json:"endedAt"`
}

// JobLogView is what a job has said since the window was last told.
type JobLogView struct {
	ID    string   `json:"id"`
	Lines []string `json:"lines"`
}

// JobService is what the window uses to see and stop what is running.
//
// Every exported method here becomes a binding, which is why pushing lives in
// JobWatcher and not on this type: Watch never returns, and a window able to
// call it could hold a goroutine open for the life of the application by
// accident. This half answers questions; the other half talks without being
// asked.
//
// It assembles no SQL and carries no credential: a job is a title, a state and
// a number, and the log it hands over has already had its secrets taken out on
// the way into the queue.
type JobService struct {
	queue   *job.Queue
	history store.JobHistory
	warning string
}

// NewJobService creates the service bound to the frontend.
//
// The history is handed in rather than reached for, like the vault and the
// engine: which file it is, or whether it is a file at all, is decided by the
// command that wires the application together.
func NewJobService(queue *job.Queue, history store.JobHistory, options ...JobServiceOption) *JobService {
	service := &JobService{queue: queue, history: history}
	for _, option := range options {
		option(service)
	}

	return service
}

// JobServiceOption configures the service.
type JobServiceOption func(*JobService)

// WithHistoryWarning gives the service something to tell the person about the
// history it was handed.
//
// A string rather than a function, unlike the vault's: where the history is
// kept is settled before the window opens, so there is nothing still on its
// way. Empty means the history is a file and it is being written.
func WithHistoryWarning(warning string) JobServiceOption {
	return func(s *JobService) { s.warning = warning }
}

// Observing adapts an emitter to the queue's observer, so that a state change
// reaches the window as it happens rather than at the next sample.
//
// It lives here rather than in the queue because the queue may not name an
// event, and here rather than in the command that wires things together
// because the name of the event belongs beside the ones above.
func Observing(emit Emitter) job.Observer {
	return func(view job.View) {
		emit.Emit(stateEvent, viewOfJob(view))
	}
}

// List answers every job the window knows about: what is running in this
// session, and what ran in the ones before it.
//
// It is the reconciliation half of ADR-0015: events are the fast path and this
// is the truth. A window that missed an event is corrected by asking, and the
// path that corrects it is the same one that filled the panel in the first
// place — so it is exercised every time the panel opens, rather than only when
// something has already gone wrong.
//
// The queue is asked first and its answer wins. A job that has just ended is
// in both places, and the copy in memory is the one the events have been
// describing; taking the other would redraw the row from the file it was
// written to a moment ago.
//
// One page of history, not the whole of it. A person who has been backing up
// nightly for a year has a history no window shows at once, and reading it to
// fill a panel is the budget this application is held to.
func (s *JobService) List(ctx context.Context) ([]JobView, error) {
	held := s.queue.List()

	views := make([]JobView, 0, len(held))
	running := make(map[string]bool, len(held))
	for _, one := range held {
		running[one.ID] = true
		views = append(views, viewOfJob(one))
	}

	remembered, err := s.history.Recent(ctx, store.Page{})
	if err != nil {
		return nil, fmt.Errorf("reading what has already run: %w", err)
	}

	for _, record := range remembered {
		if running[record.ID] {
			continue
		}

		views = append(views, viewOfRecord(record))
	}

	return views, nil
}

// HistoryView says whether what runs is being remembered, and warns when it is
// not.
//
// The warning is prose because it is shown to a person and has to name the file
// it is about. It is empty exactly when the history is being written, which is
// what the window decides whether to draw a banner from.
type HistoryView struct {
	Warning string `json:"warning"`
}

// HistoryStatus says whether what happens in this session will still be there
// tomorrow.
//
// The window asks so that it can say when the answer is no. A history that
// silently stopped being kept is the failure this is here to make visible:
// nobody finds out until the morning they look for the backup that ran
// overnight and the panel is empty.
func (s *JobService) HistoryStatus() HistoryView {
	return HistoryView{Warning: s.warning}
}

// JobCursor is where a page of the history stopped, as the window holds it.
//
// The time crosses as the string it was drawn from rather than as a number,
// so that the window hands back exactly what it was given and nothing has to
// be reassembled from two halves that could disagree.
type JobCursor struct {
	EndedAt string `json:"endedAt"`
	ID      string `json:"id"`
}

// Older answers the jobs that ended before the row the window already has.
//
// It is what makes the history walkable: List answers what is running plus the
// most recent page, which is the panel on opening, and this is what a person
// asks for when the answer they want is further back than that. An empty
// answer is the end of the history rather than a failure — there is nothing
// older.
//
// A page at a time, by the row it left off at rather than by how many rows to
// skip: the history grows while somebody reads it, and counting from the start
// would show one row twice and miss the one after it.
func (s *JobService) Older(ctx context.Context, after JobCursor) ([]JobView, error) {
	cursor, err := cursorOf(after)
	if err != nil {
		return nil, err
	}

	remembered, err := s.history.Recent(ctx, store.Page{After: cursor})
	if err != nil {
		return nil, fmt.Errorf("reading what ran before: %w", err)
	}

	views := make([]JobView, 0, len(remembered))
	for _, record := range remembered {
		views = append(views, viewOfRecord(record))
	}

	return views, nil
}

// cursorOf reads back what the window was given. The zero cursor is the
// newest, which is what a window asking without one means.
func cursorOf(after JobCursor) (store.Cursor, error) {
	if after.EndedAt == "" {
		return store.Cursor{}, nil
	}

	ended, err := time.Parse(time.RFC3339Nano, after.EndedAt)
	if err != nil {
		return store.Cursor{}, fmt.Errorf("reading the place to carry on from: %w", err)
	}

	return store.Cursor{Ended: ended, ID: after.ID}, nil
}

// Log answers everything a job has said.
//
// The whole log, unlike the events, because this is what a panel being opened
// on a job that has been running for ten minutes needs — and what a panel
// opened on last night's failure needs, which is why a job the queue no longer
// holds is looked for in the history rather than reported as gone.
func (s *JobService) Log(ctx context.Context, id string) ([]string, error) {
	lines, err := s.queue.Log(id)
	if err == nil {
		return lines, nil
	}
	if !errors.Is(err, job.ErrNotFound) {
		return nil, fmt.Errorf("reading the log of job %s: %w", id, err)
	}

	remembered, err := s.history.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the log of job %s: %w", id, err)
	}

	return remembered.Log, nil
}

// Cancel asks a job to stop.
func (s *JobService) Cancel(id string) error {
	if err := s.queue.Cancel(id); err != nil {
		return fmt.Errorf("cancelling job %s: %w", id, err)
	}

	return nil
}

// Forget drops a job that has ended from the list, and from the history.
//
// Both, because the row a person dismissed must not come back when the panel
// is next opened — and either alone, because a job that ended in this session
// is in both places while a job from last week is only in one. It is a failure
// only when neither knew it, which is a row that was never there.
func (s *JobService) Forget(ctx context.Context, id string) error {
	fromQueue := s.queue.Forget(id)
	if fromQueue != nil && !errors.Is(fromQueue, job.ErrNotFound) {
		return fmt.Errorf("forgetting job %s: %w", id, fromQueue)
	}

	fromHistory := s.history.Forget(ctx, id)
	if fromHistory != nil && !errors.Is(fromHistory, store.ErrNotFound) {
		return fmt.Errorf("forgetting job %s: %w", id, fromHistory)
	}

	if fromQueue != nil && fromHistory != nil {
		return fmt.Errorf("forgetting job %s: %w", id, fromQueue)
	}

	return nil
}

// Running is what the watcher needs from a queue: where the jobs are, and what
// they have said since it last looked.
//
// Narrow and declared here, where it is consumed, for the same reason the
// emitter above is an interface: this is the half of the pair that reads on a
// timer, and a test about what it does not ask for needs something to ask.
type Running interface {
	List() []job.View
	LogSince(id string, seq int) ([]string, int, error)
}

// JobWatcherOption configures a watcher.
type JobWatcherOption func(*JobWatcher)

// WithSampleEvery sets how often the window is told where the jobs are.
func WithSampleEvery(every time.Duration) JobWatcherOption {
	return func(w *JobWatcher) { w.every = every }
}

// JobWatcher tells the window where the jobs are, without being asked.
//
// Deliberately not a bound service. Nothing here is a question the frontend
// asks, and everything here would become a binding if it were.
type JobWatcher struct {
	queue Running
	emit  Emitter
	every time.Duration

	// What the window has already been told, so that a sample says only what
	// changed. Without it the window is woken for every job in the history on
	// every tick, for ever.
	//
	// The log is remembered as a sequence rather than as a count of lines
	// held: a full log keeps its length while it loses its beginning, so a
	// count would stop moving exactly when the job is at its most talkative.
	mu    sync.Mutex
	said  map[string]JobProgressView
	lines map[string]int
	// settled is the jobs there is nothing left to ask about: they have ended
	// and everything they said has been sent. Without it the panel's history
	// is re-read ten times a second for ever — a job that finished this
	// morning is asked what it has said since, all afternoon.
	settled map[string]bool
}

// NewJobWatcher creates the watcher.
//
// It starts nothing. Watch is what begins sampling, and the caller decides
// when that stops.
func NewJobWatcher(queue Running, emit Emitter, options ...JobWatcherOption) *JobWatcher {
	watcher := &JobWatcher{
		queue:   queue,
		emit:    emit,
		every:   defaultSampleEvery,
		said:    make(map[string]JobProgressView),
		lines:   make(map[string]int),
		settled: make(map[string]bool),
	}

	for _, option := range options {
		option(watcher)
	}

	return watcher
}

// Watch tells the window where the jobs are, until the caller gives up.
func (s *JobWatcher) Watch(ctx context.Context) {
	ticker := time.NewTicker(s.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sample()
		}
	}
}

// Sample tells the window what has changed since the last time it was told.
//
// Exported so that the cap is testable without waiting for a clock: what makes
// a thousand reports one event is that a sample reads where a job is now, and
// that is a property of this function rather than of the ticker above it.
func (s *JobWatcher) Sample() {
	held := s.queue.List()

	for _, one := range held {
		s.progressOf(one)
		s.logOf(one)
	}

	s.prune(held)
}

// prune drops what was remembered about jobs the queue no longer has.
//
// Here rather than in Forget, so that the two halves stay apart: what the
// window has been told is this type's business, and a service that reached in
// to clear it would be the coupling the split was made to avoid.
func (s *JobWatcher) prune(held []job.View) {
	alive := make(map[string]bool, len(held))
	for _, one := range held {
		alive[one.ID] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for id := range s.said {
		if !alive[id] {
			delete(s.said, id)
			delete(s.lines, id)
			delete(s.settled, id)
		}
	}
}

// progressOf tells the window where a job is, if that has moved.
func (s *JobWatcher) progressOf(one job.View) {
	moved := progressOfJob(one.Progress)

	s.mu.Lock()
	same := s.said[one.ID] == moved
	if !same {
		s.said[one.ID] = moved
	}
	s.mu.Unlock()

	if same {
		return
	}

	// Without the state. A sample reads where a job was a moment before it is
	// emitted, so one taken just before a job ends arrives after the state
	// change that announced the end — and a progress event carrying "running"
	// would put a Stop button back on a job that has finished. Transitions are
	// announced as they happen and that is the only thing that says what a job
	// is; this says where it got to.
	s.emit.Emit(progressEvent, JobView{
		ID:       one.ID,
		Progress: moved,
		Dropped:  one.Dropped,
	})
}

// logOf tells the window what a job has said since it was last told.
//
// Only the new lines. Sending the whole log every sample would send a megabyte
// a tick for a verbose restore, which is the budget the ceiling on the log was
// put there to keep.
func (s *JobWatcher) logOf(one job.View) {
	s.mu.Lock()
	settled, told := s.settled[one.ID], s.lines[one.ID]
	s.mu.Unlock()

	if settled {
		return
	}

	fresh, next, err := s.queue.LogSince(one.ID, told)
	if err != nil {
		// The job was forgotten between the listing and now. There is nothing
		// to say about a job that is gone.
		return
	}

	s.mu.Lock()
	s.lines[one.ID] = next
	// A job that has ended and had nothing new to say has said everything it
	// ever will. Nothing can be added to its log, so it is not asked again.
	if one.State.Over() && len(fresh) == 0 {
		s.settled[one.ID] = true
	}
	s.mu.Unlock()

	if len(fresh) == 0 {
		return
	}

	s.emit.Emit(logEvent, JobLogView{ID: one.ID, Lines: fresh})
}

func viewOfJob(one job.View) JobView {
	return JobView{
		ID:        one.ID,
		Kind:      one.Kind,
		Title:     one.Title,
		State:     one.State.String(),
		Err:       one.Err,
		Progress:  progressOfJob(one.Progress),
		Dropped:   one.Dropped,
		StartedAt: timeOf(one.Started),
		EndedAt:   timeOf(one.Ended),
	}
}

// viewOfRecord is a job the history remembers, as the window draws it.
//
// No progress: a job that ended is at its end, and a bar is a question about
// something still moving. What is kept is what somebody looks for afterwards —
// what it was, how it ended, when, and how much of its log was lost.
func viewOfRecord(record store.JobRecord) JobView {
	return JobView{
		ID:        record.ID,
		Kind:      record.Kind,
		Title:     record.Title,
		State:     record.State.String(),
		Err:       record.Err,
		Dropped:   record.Dropped,
		StartedAt: timeOf(record.Started),
		EndedAt:   timeOf(record.Ended),
	}
}

func progressOfJob(said job.ProgressView) JobProgressView {
	return JobProgressView{
		Step:          said.Step,
		Unit:          said.Unit,
		Done:          said.Done,
		Total:         said.Total,
		Fraction:      said.Fraction,
		Indeterminate: said.Indeterminate,
		ElapsedMs:     said.Elapsed.Milliseconds(),
		RemainingMs:   said.Remaining.Milliseconds(),
	}
}

// timeOf answers a time the window can read, and an empty string for a time
// there is not.
//
// Always in UTC, because the window sorts its rows by comparing these strings
// and the two halves of the list come from different places: a running job
// carries the clock of this machine, a finished one comes back from a file
// that keeps UTC. Left in local time, the same instant reads as two different
// strings, and "16:00+02:00" sorts after "15:00Z" although it is the earlier
// of the two. Rendering it in the reader's own zone is the window's business
// and it has the instant to do it with.
func timeOf(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.UTC().Format(time.RFC3339Nano)
}
