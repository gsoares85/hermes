package ui_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
	"github.com/gsoares85/hermes/internal/ui"
)

// recorder is the window, as far as the service can tell: something that takes
// an event and a payload. It is the whole reason the emitter is handed in
// rather than fetched — a service that called application.Get() would need a
// running Wails application to be tested at all.
type recorder struct {
	mu     sync.Mutex
	events []recorded
}

type recorded struct {
	name string
	data any
}

func (r *recorder) Emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, recorded{name: name, data: data})
}

func (r *recorder) all() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]recorded(nil), r.events...)
}

func (r *recorder) named(name string) []recorded {
	var kept []recorded
	for _, event := range r.all() {
		if event.name == name {
			kept = append(kept, event)
		}
	}

	return kept
}

type runnerFunc func(ctx context.Context, report job.Reporter) error

func (f runnerFunc) Run(ctx context.Context, report job.Reporter) error { return f(ctx, report) }

func spec(title string) job.Spec { return job.Spec{Kind: "test", Title: title} }

// finished submits work and waits for it to end.
func finished(t *testing.T, queue *job.Queue, run job.Runner) string {
	t.Helper()

	id, err := queue.Submit(spec("a job"), run)
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := queue.Wait(ctx, id); err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	return id
}

// listedJobs is the panel asking what there is, in a test that is not about
// the asking failing.
func listedJobs(t *testing.T, service *ui.JobService) []ui.JobView {
	t.Helper()

	jobs, err := service.List(t.Context())
	if err != nil {
		t.Fatalf("List(...) = _, %v, want no error", err)
	}

	return jobs
}

func TestTheServiceListsWhatIsRunning(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	if _, err := queue.Submit(
		job.Spec{Kind: "backup", Title: "shop on db.example.com"},
		runnerFunc(func(ctx context.Context, _ job.Reporter) error {
			<-ctx.Done()

			return ctx.Err()
		}),
	); err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	var listed ui.JobView
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs := listedJobs(t, service)
		if len(jobs) == 1 && jobs[0].State == "running" {
			listed = jobs[0]

			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if listed.Kind != "backup" || listed.Title != "shop on db.example.com" {
		t.Errorf("the job is %q/%q, want backup/shop on db.example.com", listed.Kind, listed.Title)
	}

	if listed.State != "running" {
		t.Errorf("the job is %q, want %q", listed.State, "running")
	}
}

// The state crosses as the name, not as the number. A number would make the
// frontend carry a copy of an enumeration whose order is a Go implementation
// detail, and reordering the constants would silently relabel every row.
func TestTheStateCrossesAsAName(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	jobs := listedJobs(t, service)
	if len(jobs) != 1 || jobs[0].State != "done" {
		t.Fatalf("the jobs are %+v, want one that is done", jobs)
	}
}

// A job that has not ended has no end, and an empty string is how that is
// said. The zero time rendered is 1 January year 1, which a date formatter in
// the window will happily print.
func TestAJobThatHasNotEndedSaysSo(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	started := make(chan struct{})
	if _, err := queue.Submit(spec("a job"), runnerFunc(func(ctx context.Context, _ job.Reporter) error {
		close(started)
		<-ctx.Done()

		return ctx.Err()
	})); err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	jobs := listedJobs(t, service)
	if len(jobs) != 1 {
		t.Fatalf("the jobs are %+v, want one", jobs)
	}

	if jobs[0].EndedAt != "" {
		t.Errorf("EndedAt = %q for a running job, want empty", jobs[0].EndedAt)
	}

	if jobs[0].StartedAt == "" {
		t.Error("StartedAt is empty for a running job, want when it began")
	}
}

// Durations cross as milliseconds. A Go duration marshals as a count of
// nanoseconds, which is a number the window would have to know to divide by a
// billion — and dividing by the wrong power of ten is invisible until an ETA
// reads "3 hours" for a job with three seconds left.
func TestDurationsCrossAsMilliseconds(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		report.Report(job.Progress{Done: 1, Total: 4, Unit: "rows", Step: "copying"})

		return nil
	}))

	jobs := listedJobs(t, service)
	if len(jobs) != 1 {
		t.Fatalf("the jobs are %+v, want one", jobs)
	}

	if jobs[0].Progress.Step != "copying" || jobs[0].Progress.Unit != "rows" {
		t.Errorf("the progress is %+v, want the step and unit it reported", jobs[0].Progress)
	}

	if jobs[0].Progress.ElapsedMs < 0 {
		t.Errorf("ElapsedMs = %d, want a count of milliseconds", jobs[0].Progress.ElapsedMs)
	}
}

func TestCancellingThroughTheService(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	started := make(chan struct{})
	id, err := queue.Submit(spec("a job"), runnerFunc(func(ctx context.Context, _ job.Reporter) error {
		close(started)
		<-ctx.Done()

		return ctx.Err()
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	if stopping := service.Cancel(id); stopping != nil {
		t.Fatalf("Cancel(...) = %v, want no error", stopping)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	view, err := queue.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if view.State != job.Cancelled {
		t.Errorf("the job is %v, want %v", view.State, job.Cancelled)
	}
}

func TestForgettingAJobThatHasEnded(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	id := finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	if err := service.Forget(t.Context(), id); err != nil {
		t.Fatalf("Forget(...) = %v, want no error", err)
	}

	if jobs := listedJobs(t, service); len(jobs) != 0 {
		t.Errorf("the jobs are %+v, want none", jobs)
	}
}

// Forgetting a job that is still running would take the row away and leave the
// work going, with nothing on screen able to stop it — the state the tab bar
// was built to prevent, in another shape.
func TestForgettingAJobThatIsStillRunning(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	started := make(chan struct{})
	id, err := queue.Submit(spec("a job"), runnerFunc(func(ctx context.Context, _ job.Reporter) error {
		close(started)
		<-ctx.Done()

		return ctx.Err()
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	if err := service.Forget(t.Context(), id); err == nil {
		t.Error("Forget(...) = nil for a running job, want an error")
	}

	if jobs := listedJobs(t, service); len(jobs) != 1 {
		t.Errorf("the jobs are %+v, want the running one still there", jobs)
	}
}

// A state change is what takes a bar off the screen, so it is emitted as it
// happens rather than at the next sample. ADR-0015 is explicit about it: the
// throttle is for progress, not for transitions.
func TestAStateChangeIsAnnouncedAsItHappens(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue(job.WithObserver(ui.Observing(into)))

	finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	states := into.named("job:state")
	if len(states) < 2 {
		t.Fatalf("the window was told of %d state changes, want at least running and done", len(states))
	}

	var seen []string
	for _, event := range states {
		view, is := event.data.(ui.JobView)
		if !is {
			t.Fatalf("a state event carried %T, want a JobView", event.data)
		}

		seen = append(seen, view.State)
	}

	if seen[len(seen)-1] != "done" {
		t.Errorf("the last state announced was %q, want %q: %v", seen[len(seen)-1], "done", seen)
	}
}

// The cap ADR-0015 put on emission, on the surface it was written for.
//
// A COPY of a million rows reports per row. Every one of those crossing to the
// webview is the window not repainting, which is the rule this whole package
// is under.
func TestAThousandReportsDoNotBecomeAThousandEvents(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	watcher := ui.NewJobWatcher(queue, into)

	reported := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	if _, err := queue.Submit(spec("a job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		for done := range int64(1000) {
			report.Report(job.Progress{Done: done + 1, Total: 1000, Unit: "rows"})
		}

		close(reported)
		<-release

		return nil
	})); err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-reported

	// Sampling is what the cap is: the loop looks at where each job is now,
	// and a thousand reports between two looks are one look's worth.
	watcher.Sample()
	watcher.Sample()

	progress := into.named("job:progress")
	if len(progress) > 2 {
		t.Errorf("the window was told %d times, want at most one per sample", len(progress))
	}

	if len(progress) == 0 {
		t.Error("the window was told nothing, want the progress that was reported")
	}
}

// Sampling a job nothing has happened to says nothing. Otherwise the window is
// woken for every job in the history, every tick, for ever.
func TestSamplingSaysNothingWhenNothingChanged(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	watcher := ui.NewJobWatcher(queue, into)

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		report.Report(job.Progress{Done: 1, Total: 1})

		return nil
	}))

	watcher.Sample()
	before := len(into.all())

	watcher.Sample()
	watcher.Sample()

	if after := len(into.all()); after != before {
		t.Errorf("sampling an unchanged job told the window %d more times, want none", after-before)
	}
}

func TestNewLogLinesReachTheWindow(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	watcher := ui.NewJobWatcher(queue, into)

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		if _, err := fmt.Fprintln(report.Log(), "pg_dump: dumping contents of table public.orders"); err != nil {
			return err
		}

		return nil
	}))

	watcher.Sample()

	logs := into.named("job:log")
	if len(logs) != 1 {
		t.Fatalf("the window was told %d times about the log, want once", len(logs))
	}

	told, is := logs[0].data.(ui.JobLogView)
	if !is {
		t.Fatalf("the log event carried %T, want a JobLogView", logs[0].data)
	}

	if len(told.Lines) != 1 || !strings.Contains(told.Lines[0], "public.orders") {
		t.Errorf("the window was told %q, want the line that was written", told.Lines)
	}
}

// Only what is new. Sending the whole log every sample would send a megabyte
// per tick for a verbose restore, which is the budget this cap exists under.
func TestOnlyTheNewLinesAreSent(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	watcher := ui.NewJobWatcher(queue, into)

	release := make(chan struct{})
	started := make(chan struct{})
	wrote := make(chan struct{})

	if _, err := queue.Submit(spec("a job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		if _, err := fmt.Fprintln(report.Log(), "first"); err != nil {
			return err
		}

		close(started)
		<-wrote

		if _, err := fmt.Fprintln(report.Log(), "second"); err != nil {
			return err
		}

		close(release)

		return nil
	})); err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started
	watcher.Sample()

	close(wrote)
	<-release
	watcher.Sample()

	logs := into.named("job:log")
	if len(logs) != 2 {
		t.Fatalf("the window was told %d times, want twice", len(logs))
	}

	second, is := logs[1].data.(ui.JobLogView)
	if !is {
		t.Fatalf("the log event carried %T, want a JobLogView", logs[1].data)
	}

	if len(second.Lines) != 1 || second.Lines[0] != "second" {
		t.Errorf("the second telling carried %q, want only the new line", second.Lines)
	}
}

// Watching stops when whoever started it gives up. A goroutine sampling for
// ever is one the application cannot close over.
func TestWatchingStopsWhenItIsToldTo(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	watcher := ui.NewJobWatcher(queue, into, ui.WithSampleEvery(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		watcher.Watch(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("Watch did not return when its context was cancelled")
	}
}

func TestAskingAboutAJobTheQueueDoesNotHave(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	service := ui.NewJobService(queue, store.NewJobMemory())

	if err := service.Cancel("no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Cancel(...) = %v, want %v", err, job.ErrNotFound)
	}

	if err := service.Forget(t.Context(), "no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Forget(...) = %v, want %v", err, job.ErrNotFound)
	}
}

// The panel of a session that has just started: nothing is running, and what
// ran before is there to be read. Without this the history is a file nobody
// ever sees.
func TestTheServiceListsWhatRanBefore(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	remember(t, history, lastNight("last-night", job.Failed))

	service := ui.NewJobService(job.NewQueue(), history)

	jobs := listedJobs(t, service)
	if len(jobs) != 1 {
		t.Fatalf("the panel shows %+v, want the job that ran last night", jobs)
	}
	if jobs[0].ID != "last-night" || jobs[0].State != "failed" {
		t.Errorf("the row is %+v, want the failed job from last night", jobs[0])
	}
	if jobs[0].Err == "" {
		t.Error("the row carries no error: a failure whose reason is gone cannot be acted on")
	}
	if jobs[0].EndedAt == "" {
		t.Error("the row has no end: a job in the history has one by definition")
	}
}

// A job that has just ended is in the queue and in the history at once, and
// the panel must show one row. The copy in memory is the one the events have
// been describing, so it is the one that wins.
func TestAJobInBothPlacesIsOneRow(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	history := store.NewJobMemory()
	service := ui.NewJobService(queue, history)

	id := finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))
	remember(t, history, lastNight(id, job.Failed))

	jobs := listedJobs(t, service)
	if len(jobs) != 1 {
		t.Fatalf("the panel shows %d rows for one job: %+v", len(jobs), jobs)
	}
	if jobs[0].State != "done" {
		t.Errorf("the row is %q, want the state the queue holds", jobs[0].State)
	}
}

// A history that cannot be read is said out loud rather than shown as an empty
// panel. An empty panel is a sentence too — "nothing has ever run" — and it is
// the wrong one.
func TestAHistoryThatCannotBeReadIsReported(t *testing.T) {
	t.Parallel()

	service := ui.NewJobService(job.NewQueue(), unreadableHistory{})

	if _, err := service.List(t.Context()); err == nil {
		t.Error("List(...) = _, nil for a history that cannot be read, want the failure")
	}
}

// The log of last night's failure is the reason somebody opens the panel at
// all. The queue has forgotten the job; the history has not.
func TestTheLogOfAJobOnlyTheHistoryRemembers(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	remembered := lastNight("last-night", job.Failed)
	remembered.Log = []string{"pg_dump: error: connection to server failed"}
	remember(t, history, remembered)

	service := ui.NewJobService(job.NewQueue(), history)

	lines, err := service.Log(t.Context(), "last-night")
	if err != nil {
		t.Fatalf("Log(...) = _, %v, want no error", err)
	}
	if len(lines) != 1 || lines[0] != remembered.Log[0] {
		t.Errorf("the log reads %q, want what the job said", lines)
	}
}

// Dismissing a row takes it out of both places. Out of one only, and the row
// somebody dismissed is back the next time the panel is opened.
func TestForgettingTakesAJobOutOfTheHistoryToo(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	history := store.NewJobMemory()
	service := ui.NewJobService(queue, history)

	id := finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))
	remember(t, history, lastNight(id, job.Done))

	if err := service.Forget(t.Context(), id); err != nil {
		t.Fatalf("Forget(...) = %v, want no error", err)
	}

	if jobs := listedJobs(t, service); len(jobs) != 0 {
		t.Errorf("the panel shows %+v after the row was dismissed", jobs)
	}
}

// A job this session never ran can still be dismissed: it is in the history
// and nowhere else, which is what every row of an old session is.
func TestForgettingAJobOnlyTheHistoryHas(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	remember(t, history, lastNight("last-night", job.Done))

	service := ui.NewJobService(job.NewQueue(), history)

	if err := service.Forget(t.Context(), "last-night"); err != nil {
		t.Fatalf("Forget(...) = %v, want no error", err)
	}
	if jobs := listedJobs(t, service); len(jobs) != 0 {
		t.Errorf("the panel shows %+v after the row was dismissed", jobs)
	}
}

// unreadableHistory is a file that has gone wrong under a running application.
type unreadableHistory struct {
	store.JobHistory
}

func (unreadableHistory) Recent(context.Context, store.Page) ([]store.JobRecord, error) {
	return nil, errors.New("the file is not a database any more")
}

func lastNight(id string, state job.State) store.JobRecord {
	return store.JobRecord{
		ID:      id,
		Kind:    "backup",
		Title:   "shop on db.example.com",
		State:   state,
		Err:     "pg_dump exited with status 1",
		Started: time.Date(2026, time.September, 11, 3, 0, 0, 0, time.UTC),
		Ended:   time.Date(2026, time.September, 11, 3, 12, 0, 0, time.UTC),
	}
}

func remember(t *testing.T, history store.JobHistory, record store.JobRecord) {
	t.Helper()

	if err := history.Save(t.Context(), record); err != nil {
		t.Fatalf("filling the history: %v", err)
	}
}

// A log that has reached its ceiling is a log that is still being written. It
// is the case the ceiling exists for — pg_restore --verbose, ten thousand
// lines — and it is the one where the window used to stop hearing anything:
// the count of lines held stops growing when old ones leave by the front, so
// anything that treats that count as a place in the stream never moves again.
func TestNewLinesKeepArrivingAfterTheLogIsFull(t *testing.T) {
	t.Parallel()

	const ceiling = 10

	into := &recorder{}
	queue := job.NewQueue(job.WithLogLines(ceiling))
	watcher := ui.NewJobWatcher(queue, into)

	filled := make(chan struct{})
	more := make(chan struct{})
	done := make(chan struct{})

	if _, err := queue.Submit(spec("a verbose job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		for i := range ceiling {
			if _, err := fmt.Fprintf(report.Log(), "filling %d\n", i); err != nil {
				return err
			}
		}

		close(filled)
		<-more

		for i := range 7 {
			if _, err := fmt.Fprintf(report.Log(), "after the ceiling %d\n", i); err != nil {
				return err
			}
		}

		close(done)

		return nil
	})); err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-filled
	watcher.Sample()
	sent := len(into.named("job:log"))

	close(more)
	<-done
	watcher.Sample()

	var arrived int
	for _, event := range into.named("job:log")[sent:] {
		view, is := event.data.(ui.JobLogView)
		if !is {
			t.Fatalf("a log event carried %T, want a JobLogView", event.data)
		}

		arrived += len(view.Lines)
	}

	if arrived != 7 {
		t.Errorf("the window was told %d lines written after the log filled up, want 7", arrived)
	}
}

// The panel sorts its rows by comparing these strings, and the two halves of
// the list come from different places: a running job from the clock of this
// machine, a finished one from a file that keeps UTC. Formatted in local time,
// the same instant reads as two different strings and the comparison puts them
// in the wrong order — on every machine that is not on UTC, which is most of
// them.
func TestEveryTimeCrossesInTheSameZone(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	history := store.NewJobMemory()
	service := ui.NewJobService(queue, history)

	finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))
	remember(t, history, lastNight("last-night", job.Done))

	for _, view := range listedJobs(t, service) {
		for _, at := range []struct{ what, value string }{
			{"started", view.StartedAt},
			{"ended", view.EndedAt},
		} {
			if at.value == "" {
				continue
			}
			if !strings.HasSuffix(at.value, "Z") {
				t.Errorf("%s of %q reads %q, want an instant in UTC: the panel compares these as text",
					at.what, view.Title, at.value)
			}
		}
	}
}

// A job that ended and said everything it was going to say is done being
// asked. The panel keeps the history of the session on screen, and sampling
// reads every row of it ten times a second: a morning's jobs would be asked
// what they had said since, all afternoon, for ever.
func TestAFinishedJobIsNotAskedAgain(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	asked := &countingQueue{Running: queue}
	watcher := ui.NewJobWatcher(asked, &recorder{})

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		_, err := report.Log().Write([]byte("all it had to say\n"))

		return err
	}))

	// The samples that deliver the last of the log, then settle it.
	watcher.Sample()
	watcher.Sample()

	settled := asked.count()

	for range 5 {
		watcher.Sample()
	}

	if got := asked.count(); got != settled {
		t.Errorf("a finished job was asked for its log %d more times, want none", got-settled)
	}
}

// countingQueue is a queue that remembers how often it was asked for a log.
type countingQueue struct {
	ui.Running

	mu    sync.Mutex
	asked int
}

func (c *countingQueue) LogSince(id string, seq int) ([]string, int, error) {
	c.mu.Lock()
	c.asked++
	c.mu.Unlock()

	return c.Running.LogSince(id, seq)
}

func (c *countingQueue) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.asked
}

// The history is walkable: what the panel shows on opening is the newest page,
// and this is how somebody reaches what came before it. Without it the keyset
// paging underneath is built, tested and unreachable — fifty rows and no way
// to the fifty-first.
func TestTheHistoryCanBeWalkedBackwards(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	for i := range store.DefaultPageSize + 3 {
		record := lastNight(fmt.Sprintf("job-%03d", i), job.Done)
		record.Ended = record.Ended.Add(time.Duration(i) * time.Minute)
		remember(t, history, record)
	}

	service := ui.NewJobService(job.NewQueue(), history)

	first := listedJobs(t, service)
	if len(first) != store.DefaultPageSize {
		t.Fatalf("the panel opened on %d rows, want a page of %d", len(first), store.DefaultPageSize)
	}

	last := first[len(first)-1]

	older, err := service.Older(t.Context(), ui.JobCursor{EndedAt: last.EndedAt, ID: last.ID})
	if err != nil {
		t.Fatalf("Older(...) = _, %v, want no error", err)
	}
	if len(older) != 3 {
		t.Fatalf("the page after the first holds %d rows, want the 3 that were left", len(older))
	}

	// No row twice, and none missed between the two pages.
	seen := make(map[string]bool, len(first)+len(older))
	for _, view := range append(append([]ui.JobView{}, first...), older...) {
		if seen[view.ID] {
			t.Errorf("job %s is on both pages", view.ID)
		}
		seen[view.ID] = true
	}
	if len(seen) != store.DefaultPageSize+3 {
		t.Errorf("the two pages hold %d jobs between them, want %d", len(seen), store.DefaultPageSize+3)
	}
}

// The end of the history is an empty answer, not a failure. A panel that has
// reached the beginning of what happened has reached it.
func TestWalkingPastTheBeginningOfTheHistory(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	remember(t, history, lastNight("the only one", job.Done))

	service := ui.NewJobService(job.NewQueue(), history)

	only := listedJobs(t, service)[0]

	older, err := service.Older(t.Context(), ui.JobCursor{EndedAt: only.EndedAt, ID: only.ID})
	if err != nil {
		t.Fatalf("Older(...) = _, %v, want no error", err)
	}
	if len(older) != 0 {
		t.Errorf("there are %d jobs before the only one there is", len(older))
	}
}

// A cursor the window could not have been given is refused rather than read as
// the beginning, which would answer the newest page to somebody asking for the
// oldest and never end.
func TestWalkingFromSomewhereThatIsNotAPlace(t *testing.T) {
	t.Parallel()

	service := ui.NewJobService(job.NewQueue(), store.NewJobMemory())

	if _, err := service.Older(t.Context(), ui.JobCursor{EndedAt: "yesterday", ID: "one"}); err == nil {
		t.Error("Older(...) = _, nil for a cursor that names no time, want the failure")
	}
}

// A history that could not be opened leaves the session with one that dies
// with it, and the person has to be told: nobody finds out otherwise until the
// morning they look for the backup that ran overnight and the panel is empty.
func TestTheWindowIsToldWhenNothingIsBeingRemembered(t *testing.T) {
	t.Parallel()

	warning := "the job history at /home/someone/.config/hermes/hermes.db could not be opened"

	service := ui.NewJobService(job.NewQueue(), store.NewJobMemory(),
		ui.WithHistoryWarning(warning))

	if got := service.HistoryStatus(); got.Warning != warning {
		t.Errorf("the window is told %q, want %q", got.Warning, warning)
	}
}

// And says nothing when there is nothing to say. A banner that is always there
// is a banner nobody reads on the day it matters.
func TestNothingIsSaidWhenTheHistoryIsBeingKept(t *testing.T) {
	t.Parallel()

	service := ui.NewJobService(job.NewQueue(), store.NewJobMemory())

	if got := service.HistoryStatus(); got.Warning != "" {
		t.Errorf("the window is told %q, want nothing", got.Warning)
	}
}

// A job that never reports progress leaves no trace in the map of what has
// been said — the zero value is what is already there, so nothing is written.
// Everything else the watcher remembers about it was keyed on that map being
// visited, so a job that says nothing and ends fast used to leave its
// bookkeeping behind for ever.
func TestNothingIsRememberedAboutAJobThatIsGone(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	watcher := ui.NewJobWatcher(queue, &recorder{})

	id := finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		// Something in the log, nothing about progress: the shape of a job
		// that runs in less than a millisecond and never says how it is doing.
		_, err := report.Log().Write([]byte("a line\n"))

		return err
	}))

	watcher.Sample()
	watcher.Sample()

	if err := queue.Forget(id); err != nil {
		t.Fatalf("forgetting the job: %v", err)
	}

	watcher.Sample()

	if held := watcher.RememberedForTest(); held != 0 {
		t.Errorf("the watcher still remembers %d things about jobs the queue no longer has", held)
	}
}
