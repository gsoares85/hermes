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

func TestTheServiceListsWhatIsRunning(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

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
		jobs := service.List()
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

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

	finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	jobs := service.List()
	if len(jobs) != 1 || jobs[0].State != "done" {
		t.Fatalf("the jobs are %+v, want one that is done", jobs)
	}
}

// A job that has not ended has no end, and an empty string is how that is
// said. The zero time rendered is 1 January year 1, which a date formatter in
// the window will happily print.
func TestAJobThatHasNotEndedSaysSo(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

	started := make(chan struct{})
	if _, err := queue.Submit(spec("a job"), runnerFunc(func(ctx context.Context, _ job.Reporter) error {
		close(started)
		<-ctx.Done()

		return ctx.Err()
	})); err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	jobs := service.List()
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

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		report.Report(job.Progress{Done: 1, Total: 4, Unit: "rows", Step: "copying"})

		return nil
	}))

	jobs := service.List()
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

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

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

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

	id := finished(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	if err := service.Forget(id); err != nil {
		t.Fatalf("Forget(...) = %v, want no error", err)
	}

	if jobs := service.List(); len(jobs) != 0 {
		t.Errorf("the jobs are %+v, want none", jobs)
	}
}

// Forgetting a job that is still running would take the row away and leave the
// work going, with nothing on screen able to stop it — the state the tab bar
// was built to prevent, in another shape.
func TestForgettingAJobThatIsStillRunning(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

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

	if err := service.Forget(id); err == nil {
		t.Error("Forget(...) = nil for a running job, want an error")
	}

	if jobs := service.List(); len(jobs) != 1 {
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
	ui.NewJobService(queue, into)

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
	service := ui.NewJobService(queue, into)

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
	service.Sample()
	service.Sample()

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
	service := ui.NewJobService(queue, into)

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		report.Report(job.Progress{Done: 1, Total: 1})

		return nil
	}))

	service.Sample()
	before := len(into.all())

	service.Sample()
	service.Sample()

	if after := len(into.all()); after != before {
		t.Errorf("sampling an unchanged job told the window %d more times, want none", after-before)
	}
}

func TestNewLogLinesReachTheWindow(t *testing.T) {
	t.Parallel()

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

	finished(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		if _, err := fmt.Fprintln(report.Log(), "pg_dump: dumping contents of table public.orders"); err != nil {
			return err
		}

		return nil
	}))

	service.Sample()

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
	service := ui.NewJobService(queue, into)

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
	service.Sample()

	close(wrote)
	<-release
	service.Sample()

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
	service := ui.NewJobService(queue, into, ui.WithSampleEvery(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		service.Watch(ctx)
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

	into := &recorder{}
	queue := job.NewQueue()
	service := ui.NewJobService(queue, into)

	if err := service.Cancel("no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Cancel(...) = %v, want %v", err, job.ErrNotFound)
	}

	if err := service.Forget("no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Forget(...) = %v, want %v", err, job.ErrNotFound)
	}
}
