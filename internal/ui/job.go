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
}

// NewJobService creates the service bound to the frontend.
//
// The history is handed in rather than reached for, like the vault and the
// engine: which file it is, or whether it is a file at all, is decided by the
// command that wires the application together.
func NewJobService(queue *job.Queue, history store.JobHistory) *JobService {
	return &JobService{queue: queue, history: history}
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
	queue *job.Queue
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
}

// NewJobWatcher creates the watcher.
//
// It starts nothing. Watch is what begins sampling, and the caller decides
// when that stops.
func NewJobWatcher(queue *job.Queue, emit Emitter, options ...JobWatcherOption) *JobWatcher {
	watcher := &JobWatcher{
		queue: queue,
		emit:  emit,
		every: defaultSampleEvery,
		said:  make(map[string]JobProgressView),
		lines: make(map[string]int),
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

	s.emit.Emit(progressEvent, JobView{
		ID:       one.ID,
		State:    one.State.String(),
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
	told := s.lines[one.ID]
	s.mu.Unlock()

	fresh, next, err := s.queue.LogSince(one.ID, told)
	if err != nil {
		// The job was forgotten between the listing and now. There is nothing
		// to say about a job that is gone.
		return
	}

	s.mu.Lock()
	s.lines[one.ID] = next
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
func timeOf(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.Format(time.RFC3339Nano)
}
