package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
	"github.com/gsoares85/hermes/internal/core/store/storetest"
)

// The double answers the same contract the file on a user's machine will, which
// is the whole point of it: every use case that files a finished job is proven
// against this one, and shipping the SQLite store must not change what those
// proofs are worth.
func TestJobMemoryObeysTheHistoryContract(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(*testing.T) storetest.Open {
		history := store.NewJobMemory()

		return func() (store.JobHistory, error) { return history, nil }
	})
}

// Nothing outlives one history and nothing is shared between two. It is the
// property that makes this a double rather than a store: a test that left rows
// behind would be a test the next one reads.
func TestJobMemoryKeepsNothingBetweenHistories(t *testing.T) {
	t.Parallel()

	written := endedJob("kept-nothing")
	if err := store.NewJobMemory().Save(t.Context(), written); err != nil {
		t.Fatalf("saving the job: %v", err)
	}

	got, err := store.NewJobMemory().Recent(t.Context(), store.Page{})
	if err != nil {
		t.Fatalf("reading a fresh history: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a fresh history holds %d records: the first one outlived itself", len(got))
	}
}

// A page asked for beyond the end of the history is empty rather than an error.
// A panel scrolled to the bottom has reached the end of what happened, which is
// not a failure to report to anybody.
func TestJobMemoryAnswersNothingPastTheEnd(t *testing.T) {
	t.Parallel()

	history := store.NewJobMemory()
	written := endedJob("past-the-end")
	if err := history.Save(t.Context(), written); err != nil {
		t.Fatalf("saving the job: %v", err)
	}

	got, err := history.Recent(t.Context(), store.Page{After: written.Cursor()})
	if err != nil {
		t.Fatalf("reading past the end: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("reading past the last job answered %d records, want none", len(got))
	}
}

// A history asked for more than the maximum gets the maximum, so that a caller
// cannot read the whole thing by passing a large number instead of paging.
func TestPageSizeIsResolvedAndCapped(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		limit int
		want  int
	}{
		"unasked for":     {0, store.DefaultPageSize},
		"negative":        {-1, store.DefaultPageSize},
		"a screenful":     {10, 10},
		"past the cap":    {store.MaxPageSize + 1, store.MaxPageSize},
		"exactly the cap": {store.MaxPageSize, store.MaxPageSize},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := (store.Page{Limit: testCase.limit}).Size(); got != testCase.want {
				t.Errorf("a page of %d asks for %d records, want %d", testCase.limit, got, testCase.want)
			}
		})
	}
}

// The ordering, stated once and asked directly, because it is the rule the
// SQLite store will restate in SQL: the conformance suite proves the two agree,
// and this proves what they are agreeing on.
func TestOlderPlacesARecordAfterACursor(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	cursor := store.Cursor{Ended: noon, ID: "m"}

	cases := map[string]struct {
		record store.JobRecord
		want   bool
	}{
		"ended earlier":               {store.JobRecord{ID: "a", Ended: noon.Add(-time.Second)}, true},
		"ended later":                 {store.JobRecord{ID: "z", Ended: noon.Add(time.Second)}, false},
		"the same instant, lower id":  {store.JobRecord{ID: "a", Ended: noon}, true},
		"the same instant, higher id": {store.JobRecord{ID: "z", Ended: noon}, false},
		"the record the cursor is on": {store.JobRecord{ID: "m", Ended: noon}, false},
		"the same instant in a new zone": {
			store.JobRecord{ID: "a", Ended: noon.In(time.FixedZone("elsewhere", 3*60*60))},
			true,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := cursor.Older(testCase.record); got != testCase.want {
				t.Errorf("Older(%v) = %v, want %v", testCase.record.Ended, got, testCase.want)
			}
		})
	}
}

// The zero cursor names no place, so everything is on the page that begins
// there. Without it the first page of every history would be empty.
func TestTheZeroCursorHoldsEveryRecord(t *testing.T) {
	t.Parallel()

	var beginning store.Cursor

	if !beginning.IsZero() {
		t.Error("the zero cursor does not report itself as zero")
	}
	if !beginning.Older(endedJob("anything")) {
		t.Error("a record is not on the page that begins at the zero cursor")
	}
}

// The rules a record is held to, asked of the record rather than through a
// store, so that every implementation inherits the same refusals.
func TestValidateRefusesARecordTheHistoryCannotKeep(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		change func(*store.JobRecord)
		want   error
	}{
		"no identifier": {func(r *store.JobRecord) { r.ID = "" }, store.ErrNoID},
		"still running": {func(r *store.JobRecord) { r.State = job.Running }, store.ErrNotOver},
		"still pending": {func(r *store.JobRecord) { r.State = job.Pending }, store.ErrNotOver},
		"winding down":  {func(r *store.JobRecord) { r.State = job.Cancelling }, store.ErrNotOver},
		"a state nobody named": {
			func(r *store.JobRecord) { r.State = job.State(42) }, store.ErrNotOver,
		},
		"no end": {func(r *store.JobRecord) { r.Ended = time.Time{} }, store.ErrNoEnd},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			record := endedJob("refused")
			testCase.change(&record)

			if err := record.Validate(); !errors.Is(err, testCase.want) {
				t.Errorf("validating %s returned %v, want %v", name, err, testCase.want)
			}
		})
	}
}

// A job cancelled before it ever started has no beginning, and that is a real
// outcome rather than a missing value. Refusing it would lose the row that
// explains why the backup somebody asked for never happened.
func TestARecordOfAJobThatNeverStartedIsKept(t *testing.T) {
	t.Parallel()

	never := endedJob("never-started")
	never.State = job.Cancelled
	never.Started = time.Time{}

	history := store.NewJobMemory()
	if err := history.Save(t.Context(), never); err != nil {
		t.Fatalf("saving a job that never started: %v", err)
	}

	got, err := history.Recent(t.Context(), store.Page{})
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	if len(got) != 1 || !got[0].Started.IsZero() {
		t.Errorf("the history holds %v, want one record with no beginning", got)
	}
}

func endedJob(id string) store.JobRecord {
	return store.JobRecord{
		ID:      id,
		Kind:    "backup",
		Title:   "a database",
		State:   job.Done,
		Started: time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC),
		Ended:   time.Date(2026, time.September, 12, 10, 1, 0, 0, time.UTC),
	}
}
