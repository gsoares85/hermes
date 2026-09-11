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

// waited is how long a test is prepared to wait for a job the queue is
// supposed to finish. Generous, because it is only ever reached when something
// is wrong: the queue signals, so a passing test never spends it.
const waited = 5 * time.Second

// runnerFunc adapts a function to the Runner the queue takes.
type runnerFunc func(ctx context.Context) error

func (f runnerFunc) Run(ctx context.Context) error { return f(ctx) }

func spec(title string) job.Spec {
	return job.Spec{Kind: "test", Title: title}
}

// submitted runs the work and waits for it to reach an end, failing the test
// rather than hanging for ever if it never does.
func submitted(t *testing.T, queue *job.Queue, run job.Runner) job.View {
	t.Helper()

	id, err := queue.Submit(spec("a job"), run)
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	view, err := queue.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	return view
}

func TestWorkThatSucceedsIsDone(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	view := submitted(t, queue, runnerFunc(func(context.Context) error { return nil }))

	if view.State != job.Done {
		t.Errorf("the job is %v, want %v", view.State, job.Done)
	}

	if view.Err != "" {
		t.Errorf("the job reports %q, want no error", view.Err)
	}
}

func TestWorkThatReturnsAnErrorFails(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()
	boom := errors.New("the server said no")

	view := submitted(t, queue, runnerFunc(func(context.Context) error { return boom }))

	if view.State != job.Failed {
		t.Errorf("the job is %v, want %v", view.State, job.Failed)
	}

	if !strings.Contains(view.Err, boom.Error()) {
		t.Errorf("the job reports %q, want it to contain %q", view.Err, boom.Error())
	}
}

// The criterion the queue exists to meet: a job that blows up is a job that
// failed, not an application that closed. A nil dereference inside a parser of
// pg_dump output would otherwise take the window down with a backup half
// written to disk.
func TestWorkThatPanicsFailsInsteadOfTakingTheProcessDown(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	view := submitted(t, queue, runnerFunc(func(context.Context) error {
		panic("a parser met something it did not expect")
	}))

	if view.State != job.Failed {
		t.Errorf("the job is %v, want %v", view.State, job.Failed)
	}

	// What was panicked with has to survive into the message, or the person is
	// told a job failed and nothing about why.
	if !strings.Contains(view.Err, "a parser met something it did not expect") {
		t.Errorf("the job reports %q, want it to name what it panicked with", view.Err)
	}
}

// A panic unwinds one goroutine. Nothing about the queue, and nothing about
// what else is running in it, may go with it.
func TestAPanicInOneJobLeavesTheOthersAlone(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	release := make(chan struct{})
	slow, err := queue.Submit(spec("the survivor"), runnerFunc(func(context.Context) error {
		<-release

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	submitted(t, queue, runnerFunc(func(context.Context) error { panic("boom") }))

	close(release)

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	view, err := queue.Wait(ctx, slow)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if view.State != job.Done {
		t.Errorf("the surviving job is %v, want %v", view.State, job.Done)
	}
}

func TestAJobIsRunningWhileItRuns(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	started := make(chan struct{})
	release := make(chan struct{})

	id, err := queue.Submit(spec("a job"), runnerFunc(func(context.Context) error {
		close(started)
		<-release

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-started

	view, err := queue.Get(id)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if view.State != job.Running {
		t.Errorf("the job is %v while it runs, want %v", view.State, job.Running)
	}

	close(release)
}

func TestTheQueueKeepsWhatWasSubmitted(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	id, err := queue.Submit(
		job.Spec{Kind: "backup", Title: "shop on db.example.com"},
		runnerFunc(func(context.Context) error { return nil }),
	)
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	view, err := queue.Get(id)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if view.ID != id {
		t.Errorf("the job is filed under %q, want %q", view.ID, id)
	}

	if view.Kind != "backup" || view.Title != "shop on db.example.com" {
		t.Errorf("the job is %q/%q, want %q/%q",
			view.Kind, view.Title, "backup", "shop on db.example.com")
	}
}

func TestAJobNeedsAKind(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	// Without one the history cannot say what a row was, and the panel cannot
	// choose an icon for it. It is cheaper to refuse than to invent a default
	// that means nothing.
	_, err := queue.Submit(job.Spec{Title: "nameless"}, runnerFunc(func(context.Context) error {
		return nil
	}))

	if !errors.Is(err, job.ErrNoKind) {
		t.Errorf("Submit(...) = _, %v, want %v", err, job.ErrNoKind)
	}
}

func TestAJobNeedsWorkToDo(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	if _, err := queue.Submit(spec("a job"), nil); !errors.Is(err, job.ErrNoWork) {
		t.Errorf("Submit(...) = _, %v, want %v", err, job.ErrNoWork)
	}
}

func TestAskingAboutAJobTheQueueDoesNotHave(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	if _, err := queue.Get("no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Get(...) = _, %v, want %v", err, job.ErrNotFound)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	if _, err := queue.Wait(ctx, "no-such-job"); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Wait(...) = _, %v, want %v", err, job.ErrNotFound)
	}
}

// Waiting is bounded by the caller, not by the job. A backup that never
// answers must not hold whoever asked about it for ever.
func TestWaitingGivesUpWhenTheCallerDoes(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	release := make(chan struct{})
	defer close(release)

	id, err := queue.Submit(spec("a job"), runnerFunc(func(context.Context) error {
		<-release

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := queue.Wait(ctx, id); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait(...) = _, %v, want %v", err, context.Canceled)
	}
}

// A job that is already over answers at once. Without this, the panel asking
// about a finished job would wait for something that has already happened.
func TestWaitingForAJobThatIsAlreadyOver(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	view := submitted(t, queue, runnerFunc(func(context.Context) error { return nil }))

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	again, err := queue.Wait(ctx, view.ID)
	if err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}

	if again.State != job.Done {
		t.Errorf("the job is %v, want %v", again.State, job.Done)
	}
}

func TestTheQueueListsWhatItHas(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	for _, title := range []string{"first", "second", "third"} {
		if _, err := queue.Submit(spec(title), runnerFunc(func(context.Context) error {
			return nil
		})); err != nil {
			t.Fatalf("Submit(%q, ...) = _, %v, want no error", title, err)
		}
	}

	if got := len(queue.List()); got != 3 {
		t.Errorf("List() has %d jobs, want 3", got)
	}
}

// The queue is reached from the window, from the goroutine of every job, and
// from whatever is waiting. Under -race this is the test that says the lock
// covers what it must.
func TestTheQueueIsReachedFromEverywhereAtOnce(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			id, err := queue.Submit(spec("a job"), runnerFunc(func(context.Context) error {
				return nil
			}))
			if err != nil {
				t.Errorf("Submit(...) = _, %v, want no error", err)

				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), waited)
			defer cancel()

			if _, err := queue.Wait(ctx, id); err != nil {
				t.Errorf("Wait(...) = _, %v, want no error", err)
			}

			queue.List()
		})
	}

	wg.Wait()

	if got := len(queue.List()); got != 20 {
		t.Errorf("List() has %d jobs, want 20", got)
	}
}
