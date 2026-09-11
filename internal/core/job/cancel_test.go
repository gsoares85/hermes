package job_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
)

// cleaningRunner is work that honours its context and has something to undo.
type cleaningRunner struct {
	run   func(ctx context.Context, report job.Reporter) error
	clean func(ctx context.Context) error
}

func (r cleaningRunner) Run(ctx context.Context, report job.Reporter) error {
	return r.run(ctx, report)
}

func (r cleaningRunner) Cleanup(ctx context.Context) error {
	return r.clean(ctx)
}

// waitsForCancel is work that does nothing until it is told to stop, which is
// what every long operation in this product looks like from here.
func waitsForCancel(started chan<- struct{}) func(context.Context, job.Reporter) error {
	return func(ctx context.Context, _ job.Reporter) error {
		close(started)
		<-ctx.Done()

		return ctx.Err()
	}
}

// cancelled submits work, cancels it once it has begun, and waits for the end.
func cancelled(t *testing.T, queue *job.Queue, run job.Runner, started <-chan struct{}) job.View {
	t.Helper()

	id, err := queue.Submit(spec("a job"), run)
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	// Waiting for the work to say it began is what separates cancelling a job
	// that is running from cancelling one that never started. They are
	// different paths, and a test that races between them tests neither.
	if started != nil {
		<-started
	}

	if stopping := queue.Cancel(id); stopping != nil {
		t.Fatalf("Cancel(...) = %v, want no error", stopping)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	view, err := queue.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	return view
}

func TestCancellingWorkThatIsRunning(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})

	id, err := queue.Submit(spec("a job"), runnerFunc(waitsForCancel(started)))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	if stopping := queue.Cancel(id); stopping != nil {
		t.Fatalf("Cancel(...) = %v, want no error", stopping)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	view, err := queue.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if view.State != job.Cancelled {
		t.Errorf("the job is %v, want %v", view.State, job.Cancelled)
	}
}

// Work that stops because it was cancelled returns context.Canceled, and that
// is not a failure to report. Telling somebody their backup failed because
// they pressed Stop is the same defect the connection form was written to
// avoid.
func TestBeingCancelledIsNotFailing(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})

	view := cancelled(t, queue, runnerFunc(waitsForCancel(started)), started)

	if view.State != job.Cancelled {
		t.Errorf("the job is %v, want %v", view.State, job.Cancelled)
	}

	if view.Err != "" {
		t.Errorf("the job reports %q, want nothing", view.Err)
	}
}

// The state exists so the window can say "stopping…" rather than claim a job
// stopped while its subprocess is still writing.
func TestAJobSaysItIsStoppingWhileItStops(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	id, err := queue.Submit(spec("a job"), runnerFunc(func(ctx context.Context, _ job.Reporter) error {
		close(started)
		<-ctx.Done()
		<-release

		return ctx.Err()
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	if stopping := queue.Cancel(id); stopping != nil {
		t.Fatalf("Cancel(...) = %v, want no error", stopping)
	}

	view, err := queue.Get(id)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if view.State != job.Cancelling {
		t.Errorf("the job is %v while it winds down, want %v", view.State, job.Cancelling)
	}
}

// Cancelled before it was picked up. Nothing ran, so there is nothing to wind
// down — and the work must not run afterwards, which is the part that would
// start a backup somebody had already called off.
func TestCancellingWorkThatHasNotStarted(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	var ran atomicFlag
	view := cancelled(t, queue, runnerFunc(func(context.Context, job.Reporter) error {
		ran.set()

		return nil
	}), nil)

	if view.State != job.Cancelled {
		t.Errorf("the job is %v, want %v", view.State, job.Cancelled)
	}

	if ran.get() {
		t.Error("the work ran after the job was cancelled, want it never to start")
	}
}

func TestTheCleanupRunsWhenAJobIsCancelled(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})

	var cleaned atomicFlag
	view := cancelled(t, queue, cleaningRunner{
		run: waitsForCancel(started),
		clean: func(context.Context) error {
			cleaned.set()

			return nil
		},
	}, started)

	if !cleaned.get() {
		t.Error("the cleanup did not run, want it to run on cancellation")
	}

	if view.State != job.Cancelled {
		t.Errorf("the job is %v, want %v", view.State, job.Cancelled)
	}
}

// The cleanup removes the partial file and kills the process group. Running it
// while the work is still writing would race the thing it is cleaning up
// against the thing producing it.
func TestTheCleanupRunsAfterTheWorkHasStopped(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})

	var order chronicle
	cancelled(t, queue, cleaningRunner{
		run: func(ctx context.Context, _ job.Reporter) error {
			close(started)
			<-ctx.Done()
			order.record("work stopped")

			return ctx.Err()
		},
		clean: func(context.Context) error {
			order.record("cleaned")

			return nil
		},
	}, started)

	if got := order.read(); len(got) != 2 || got[0] != "work stopped" || got[1] != "cleaned" {
		t.Errorf("things happened in the order %q, want work stopped then cleaned", got)
	}
}

// A cleanup that fails is the partial file nobody removed, and that is worth
// telling somebody about even though they asked for the job to stop.
func TestACleanupThatFailsIsReported(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})
	boom := errors.New("the partial file could not be removed")

	view := cancelled(t, queue, cleaningRunner{
		run:   waitsForCancel(started),
		clean: func(context.Context) error { return boom },
	}, started)

	if view.State != job.Failed {
		t.Errorf("the job is %v, want %v", view.State, job.Failed)
	}

	if !strings.Contains(view.Err, boom.Error()) {
		t.Errorf("the job reports %q, want it to contain %q", view.Err, boom.Error())
	}
}

// The criterion with a number on it. A cleanup that hangs must not hold the
// job in cancelling for ever: the person pressed Stop, and a button that does
// nothing visible is the state this package exists to prevent.
func TestACleanupThatHangsDoesNotHoldTheJob(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue(job.WithCleanupTimeout(50 * time.Millisecond))
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	began := time.Now()
	view := cancelled(t, queue, cleaningRunner{
		run: waitsForCancel(started),
		clean: func(ctx context.Context) error {
			select {
			case <-release:
			case <-ctx.Done():
			}

			return ctx.Err()
		},
	}, started)

	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("cancelling took %v, want under 2s", took)
	}

	// The cleanup did not finish, and saying the job cancelled cleanly would
	// claim the partial file is gone.
	if view.State != job.Failed {
		t.Errorf("the job is %v, want %v", view.State, job.Failed)
	}
}

// Nothing ran, so there is nothing to undo. A cleanup for work that never
// started would remove a file somebody else is using, since the name it would
// remove was chosen before the job was.
func TestNoCleanupForWorkThatNeverStarted(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	var cleaned atomicFlag
	cancelled(t, queue, cleaningRunner{
		run: func(context.Context, job.Reporter) error { return nil },
		clean: func(context.Context) error {
			cleaned.set()

			return nil
		},
	}, nil)

	if cleaned.get() {
		t.Error("the cleanup ran for work that never started, want it not to")
	}
}

// Two people pressing Cancel on the same job is one cancellation. So is a
// retry of a call that already landed.
func TestCancellingTwiceIsCancellingOnce(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})

	var cleanups counter
	id, err := queue.Submit(spec("a job"), cleaningRunner{
		run: waitsForCancel(started),
		clean: func(context.Context) error {
			cleanups.add()

			return nil
		},
	})
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	for range 5 {
		if stopping := queue.Cancel(id); stopping != nil {
			t.Fatalf("Cancel(...) = %v, want no error", stopping)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	if _, err := queue.Wait(ctx, id); err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if got := cleanups.read(); got != 1 {
		t.Errorf("the cleanup ran %d times, want once", got)
	}
}

// A job that has finished is not something Cancel can do anything about, and
// saying so as an error would make the window report a problem for a button
// somebody pressed a moment too late.
func TestCancellingAJobThatIsAlreadyOver(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	view := submitted(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	if err := queue.Cancel(view.ID); err != nil {
		t.Errorf("Cancel(...) = %v, want no error", err)
	}

	again, err := queue.Get(view.ID)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if again.State != job.Done {
		t.Errorf("the job is %v, want it left at %v", again.State, job.Done)
	}
}

// Cancelling is a request, not a promise. Work that reaches its end in the gap
// finished, and reporting otherwise would call a backup that exists one that
// does not.
func TestWorkThatFinishesWhileItIsBeingCancelled(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})
	release := make(chan struct{})

	id, err := queue.Submit(spec("a job"), runnerFunc(func(context.Context, job.Reporter) error {
		close(started)
		<-release

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	if stopping := queue.Cancel(id); stopping != nil {
		t.Fatalf("Cancel(...) = %v, want no error", stopping)
	}

	close(release)

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	view, err := queue.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if view.State != job.Done {
		t.Errorf("the job is %v, want %v", view.State, job.Done)
	}
}

func TestCancellingAJobTheQueueDoesNotHave(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	if err := queue.Cancel("no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Cancel(...) = %v, want %v", err, job.ErrNotFound)
	}
}

// Cancel is reached from the window while the job runs in its own goroutine.
func TestCancellingFromEverywhereAtOnce(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	started := make(chan struct{})

	id, err := queue.Submit(spec("a job"), runnerFunc(waitsForCancel(started)))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if stopping := queue.Cancel(id); stopping != nil {
				t.Errorf("Cancel(...) = %v, want no error", stopping)
			}
		})
	}

	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	view, err := queue.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if view.State != job.Cancelled {
		t.Errorf("the job is %v, want %v", view.State, job.Cancelled)
	}
}

// Small guarded values, so that a test reading what work wrote from another
// goroutine is a test and not a data race.
type atomicFlag struct {
	mu   sync.Mutex
	set_ bool
}

func (f *atomicFlag) set() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.set_ = true
}

func (f *atomicFlag) get() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.set_
}

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) add() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.n++
}

func (c *counter) read() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

type chronicle struct {
	mu   sync.Mutex
	what []string
}

func (c *chronicle) record(what string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.what = append(c.what, what)
}

func (c *chronicle) read() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.what...)
}
