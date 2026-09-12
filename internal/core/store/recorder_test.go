package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
)

// A job that ends is written down, with what it said. This is the whole
// feature: the panel of the next session is filled from what this wrote.
func TestTheRecorderWritesDownAJobThatEnded(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	recorder := store.NewRecorder(history, logOf("pg_dump: dumping public.orders"), refuseFailure(t))

	recorder.Observe(endedView("finished", job.Done))

	got := onlyRecord(t, history)
	if got.ID != "finished" || got.State != job.Done {
		t.Errorf("the history holds %+v, want the job that ended", got)
	}
	if len(got.Log) != 1 || got.Log[0] != "pg_dump: dumping public.orders" {
		t.Errorf("the history kept the log %q, want what the job said", got.Log)
	}
}

// A job that is still going is not written down. A row saying "running"
// outlives the only process that could ever correct it, and the panel of the
// next session would show a backup that is not happening.
func TestTheRecorderIgnoresAJobThatHasNotEnded(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	recorder := store.NewRecorder(history, logOf(), refuseFailure(t))

	for _, state := range []job.State{job.Pending, job.Running, job.Cancelling} {
		recorder.Observe(endedView("in-flight", state))
	}

	if got := recent(t, history); len(got) != 0 {
		t.Errorf("the history holds %d records for a job that has not ended", len(got))
	}
}

// Every end is an end, including the two nobody wants. A history that kept
// only what succeeded would answer "nothing happened" for the night everything
// failed.
func TestTheRecorderWritesDownEveryWayOfEnding(t *testing.T) {
	t.Parallel()

	for _, state := range []job.State{job.Done, job.Failed, job.Cancelled} {
		t.Run(state.String(), func(t *testing.T) {
			t.Parallel()

			history := store.NewJobMemory()
			store.NewRecorder(history, logOf(), refuseFailure(t)).Observe(endedView("ended", state))

			if got := onlyRecord(t, history); got.State != state {
				t.Errorf("the history holds %v, want %v", got.State, state)
			}
		})
	}
}

// A history that cannot be written is reported and nothing else. The backup
// happened; the bookkeeping about it did not, and losing the second must not
// look like losing the first.
func TestAHistoryThatCannotBeWrittenIsReported(t *testing.T) {
	t.Parallel()

	broken := errors.New("the disk is full")

	var told []error
	store.NewRecorder(refusingHistory{err: broken}, logOf(), func(err error) {
		told = append(told, err)
	}).Observe(endedView("unwritable", job.Done))

	if len(told) != 1 {
		t.Fatalf("a failed write was reported %d times, want once", len(told))
	}
	if !errors.Is(told[0], broken) {
		t.Errorf("the report reads %v and does not carry what went wrong", told[0])
	}
	if !strings.Contains(told[0].Error(), "unwritable") {
		t.Errorf("the report reads %q and does not name the job", told[0])
	}
}

// A job forgotten between ending and being written down is not an error: the
// person dismissed it, and there is nothing left to keep.
func TestAJobWhoseLogIsGoneIsNotWrittenDown(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	recorder := store.NewRecorder(history, func(string) ([]string, error) {
		return nil, job.ErrNotFound
	}, refuseFailure(t))

	recorder.Observe(endedView("dismissed", job.Done))

	if got := recent(t, history); len(got) != 0 {
		t.Errorf("the history holds %d records for a job that was forgotten", len(got))
	}
}

// The translation from what the window shows to what tomorrow remembers,
// asked on its own: every field crosses, including the count of lines the log's
// ceiling took.
func TestRecordOfCarriesTheWholeJob(t *testing.T) {
	t.Parallel()

	view := job.View{
		ID:      "whole",
		Kind:    "restore",
		Title:   "staging from last night",
		State:   job.Failed,
		Err:     "pg_restore exited with status 1",
		Dropped: 12,
		Started: time.Date(2026, time.September, 12, 3, 0, 0, 0, time.UTC),
		Ended:   time.Date(2026, time.September, 12, 3, 40, 0, 0, time.UTC),
	}

	got := store.RecordOf(view, []string{"one", "two"})

	if got.ID != view.ID || got.Kind != view.Kind || got.Title != view.Title {
		t.Errorf("the record reads %+v, want the job", got)
	}
	if got.State != view.State || got.Err != view.Err {
		t.Errorf("the record ended as %v (%q), want %v (%q)", got.State, got.Err, view.State, view.Err)
	}
	if !got.Started.Equal(view.Started) || !got.Ended.Equal(view.Ended) {
		t.Errorf("the record ran %v to %v, want %v to %v", got.Started, got.Ended, view.Started, view.Ended)
	}
	if got.Dropped != view.Dropped {
		t.Errorf("the record dropped %d lines, want %d: a truncated log passed off as whole", got.Dropped, view.Dropped)
	}
	if len(got.Log) != 2 {
		t.Errorf("the record kept %d lines, want the two the job said", len(got.Log))
	}
}

// refusingHistory fails every write, and is asked for nothing else.
type refusingHistory struct {
	store.JobHistory
	err error
}

func (r refusingHistory) Save(context.Context, store.JobRecord) error { return r.err }

func endedView(id string, state job.State) job.View {
	return job.View{
		ID:      id,
		Kind:    "backup",
		Title:   "a database",
		State:   state,
		Started: time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC),
		Ended:   time.Date(2026, time.September, 12, 10, 1, 0, 0, time.UTC),
	}
}

func logOf(lines ...string) store.LogOf {
	return func(string) ([]string, error) { return lines, nil }
}

// refuseFailure is the reporter of a test that expects nothing to go wrong. A
// failure swallowed here would be a history that stopped being kept, which is
// the one outcome this feature exists to prevent.
func refuseFailure(t *testing.T) func(error) {
	t.Helper()

	return func(err error) { t.Errorf("writing the history failed: %v", err) }
}

func recent(t *testing.T, history store.JobHistory) []store.JobRecord {
	t.Helper()

	got, err := history.Recent(t.Context(), store.Page{})
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}

	return got
}

func onlyRecord(t *testing.T, history store.JobHistory) store.JobRecord {
	t.Helper()

	got := recent(t, history)
	if len(got) != 1 {
		t.Fatalf("the history holds %d records, want 1", len(got))
	}

	return got[0]
}
