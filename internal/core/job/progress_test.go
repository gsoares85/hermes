package job_test

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
)

// testClock is a clock a test moves by hand. Time that passes on its own is
// what makes a test about durations flaky, and every duration this package
// derives is a duration somebody reads off the screen.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(by)
}

// reporting runs work that reports once, holds the job there, and answers what
// the queue says about it while it is still running.
func reporting(t *testing.T, queue *job.Queue, clock *testClock, say job.Progress, after time.Duration) job.View {
	t.Helper()

	reported := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	id, err := queue.Submit(spec("a job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		clock.advance(after)
		report.Report(say)
		close(reported)
		<-release

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-reported

	view, err := queue.Get(id)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	return view
}

func TestAJobThatHasSaidNothingSaysNothing(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	view := submitted(t, queue, runnerFunc(func(context.Context, job.Reporter) error { return nil }))

	// Indeterminate rather than nought per cent: a job that has not reported
	// has not failed to advance, and a bar sitting at zero says it has.
	if !view.Progress.Indeterminate {
		t.Errorf("a job that reported nothing is determinate, want indeterminate")
	}

	if view.Progress.Fraction != 0 {
		t.Errorf("Fraction = %v, want 0", view.Progress.Fraction)
	}
}

func TestWhatTheWorkSaysIsWhatTheQueueReports(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	view := reporting(t, queue, clock, job.Progress{
		Step:  "copying public.orders",
		Unit:  "rows",
		Done:  25,
		Total: 100,
	}, time.Second)

	if view.Progress.Step != "copying public.orders" {
		t.Errorf("Step = %q, want %q", view.Progress.Step, "copying public.orders")
	}

	if view.Progress.Unit != "rows" {
		t.Errorf("Unit = %q, want %q", view.Progress.Unit, "rows")
	}

	if view.Progress.Done != 25 || view.Progress.Total != 100 {
		t.Errorf("Done/Total = %d/%d, want 25/100", view.Progress.Done, view.Progress.Total)
	}

	if view.Progress.Fraction != 0.25 {
		t.Errorf("Fraction = %v, want 0.25", view.Progress.Fraction)
	}
}

// A quarter of the way after one second means three seconds left. It is the
// whole of the arithmetic, and it is worth one test because everything else
// here exists to keep it from being asked in a situation where it is nonsense.
func TestHowLongIsLeft(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	view := reporting(t, queue, clock, job.Progress{Done: 25, Total: 100}, time.Second)

	if view.Progress.Elapsed != time.Second {
		t.Errorf("Elapsed = %v, want %v", view.Progress.Elapsed, time.Second)
	}

	if view.Progress.Remaining != 3*time.Second {
		t.Errorf("Remaining = %v, want %v", view.Progress.Remaining, 3*time.Second)
	}
}

func TestWhenHowLongIsLeftCannotBeAnswered(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		say  job.Progress
	}{
		// The work knows the units but not how many there are: pg_dump on a
		// table whose row count nobody asked for.
		{"a total nobody knows", job.Progress{Unit: "rows", Done: 900}},
		// Started, nothing finished. Dividing by it is what produces the
		// infinity, and an infinity rendered is "Infinityms left".
		{"nothing done yet", job.Progress{Done: 0, Total: 100}},
		// Nonsense the work should not send and the queue must survive.
		{"a negative total", job.Progress{Done: 5, Total: -1}},
		{"a negative count", job.Progress{Done: -5, Total: 100}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clock := newTestClock()
			queue := job.NewQueue(job.WithClock(clock.Now))

			view := reporting(t, queue, clock, tc.say, time.Second)

			if !view.Progress.Indeterminate {
				t.Errorf("the job is determinate, want indeterminate")
			}

			if view.Progress.Remaining != 0 {
				t.Errorf("Remaining = %v, want 0", view.Progress.Remaining)
			}

			if math.IsInf(view.Progress.Fraction, 0) || math.IsNaN(view.Progress.Fraction) {
				t.Errorf("Fraction = %v, want a number", view.Progress.Fraction)
			}
		})
	}
}

// A server that over-reports must not produce a bar past its end. pg_restore
// counts what it did, not what somebody estimated it would do, and the two
// disagree.
func TestMoreDoneThanThereWasToDo(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	view := reporting(t, queue, clock, job.Progress{Done: 150, Total: 100}, time.Second)

	if view.Progress.Fraction != 1 {
		t.Errorf("Fraction = %v, want 1", view.Progress.Fraction)
	}

	if view.Progress.Remaining != 0 {
		t.Errorf("Remaining = %v, want 0", view.Progress.Remaining)
	}
}

// The last word is the only word. A million reports from a COPY cost one
// write and are read as one value, which is what keeps reporting free enough
// that work can do it per row.
func TestTheLatestWordWins(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	reported := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	id, err := queue.Submit(spec("a job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		for done := range int64(1000) {
			report.Report(job.Progress{Done: done + 1, Total: 1000, Unit: "rows"})
		}

		close(reported)
		<-release

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	<-reported

	view, err := queue.Get(id)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if view.Progress.Done != 1000 {
		t.Errorf("Done = %d, want 1000", view.Progress.Done)
	}
}

// Elapsed stops when the job does. A finished job whose clock kept running
// would report a backup that took longer every time somebody opened the panel.
func TestTimeStopsWhenTheJobDoes(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	view := submitted(t, queue, runnerFunc(func(context.Context, job.Reporter) error {
		clock.advance(2 * time.Second)

		return nil
	}))

	if view.Progress.Elapsed != 2*time.Second {
		t.Errorf("Elapsed = %v, want %v", view.Progress.Elapsed, 2*time.Second)
	}

	clock.advance(time.Hour)

	again, err := queue.Get(view.ID)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if again.Progress.Elapsed != 2*time.Second {
		t.Errorf("Elapsed grew to %v after the job ended, want %v", again.Progress.Elapsed, 2*time.Second)
	}
}

func TestWhenAJobStartedAndWhenItEnded(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	started := clock.Now()
	queue := job.NewQueue(job.WithClock(clock.Now))

	view := submitted(t, queue, runnerFunc(func(context.Context, job.Reporter) error {
		clock.advance(time.Minute)

		return nil
	}))

	if !view.Started.Equal(started) {
		t.Errorf("Started = %v, want %v", view.Started, started)
	}

	if !view.Ended.Equal(started.Add(time.Minute)) {
		t.Errorf("Ended = %v, want %v", view.Ended, started.Add(time.Minute))
	}
}

// A job that has not ended has no end, and the zero time is how that is said.
// A caller that renders it as a date shows 1 January year 1, which is loud
// enough to catch, unlike an end silently equal to the start.
func TestAJobThatIsStillRunningHasNoEnd(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	view := reporting(t, queue, clock, job.Progress{Done: 1, Total: 2}, time.Second)

	if !view.Ended.IsZero() {
		t.Errorf("Ended = %v for a running job, want the zero time", view.Ended)
	}
}

// Work that keeps talking after it returned is work with a goroutine of its
// own that nobody joined. It must not rewrite the progress of a job somebody
// has already been told is over.
func TestWorkThatReportsAfterItIsOverIsIgnored(t *testing.T) {
	t.Parallel()

	clock := newTestClock()
	queue := job.NewQueue(job.WithClock(clock.Now))

	var late job.Reporter
	view := submitted(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		late = report
		report.Report(job.Progress{Step: "the last thing it said", Done: 1, Total: 1})

		return nil
	}))

	late.Report(job.Progress{Step: "after the end", Done: 0, Total: 1000})

	again, err := queue.Get(view.ID)
	if err != nil {
		t.Fatalf("Get(...) = _, %v, want no error", err)
	}

	if again.Progress.Step != "the last thing it said" {
		t.Errorf("Step = %q, want %q", again.Progress.Step, "the last thing it said")
	}
}

// Reporting happens in the goroutine of the work and reading happens in the
// one that draws. Under -race this is what says the two are separated.
func TestReportingAndReadingAtTheSameTime(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	id, err := queue.Submit(spec("a job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		for done := range int64(500) {
			report.Report(job.Progress{Done: done + 1, Total: 500, Unit: "rows"})
		}

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				if _, err := queue.Get(id); err != nil {
					t.Errorf("Get(...) = _, %v, want no error", err)

					return
				}

				queue.List()
			}
		})
	}

	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), waited)
	defer cancel()

	if _, err := queue.Wait(ctx, id); err != nil {
		t.Fatalf("Wait(...) = _, %v, want no error", err)
	}
}
