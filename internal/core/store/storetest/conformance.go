// Package storetest holds the conformance suite every job history must pass.
//
// The history has two implementations and they must not drift apart: the
// in-memory one the rest of the core is tested against, and the SQLite one a
// person actually accumulates history in. A double more forgiving than the
// real store proves the core against a fiction, and the difference then
// surfaces on somebody's machine instead of in the pipeline. One suite, run in
// internal/core/store against the double and in internal/sqlitestore against a
// file, is what keeps the two the same.
//
// The suite never assumes an empty history: every case saves the records it
// then asks about and identifies them by identifiers nothing else uses, so it
// answers the same against a store that has just been created and against one
// somebody has been running for a year.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
)

// Storage hands a case somewhere to keep history that no other case touches.
//
// It is called once per case, and what it answers may be called more than
// once: opening again is how the suite asks whether what was written outlived
// the thing that wrote it. For a store whose storage is a file that means
// opening the same path twice; for one whose storage is the process it means
// answering the same history, which is the honest reading of "it survived" for
// something that is not meant to.
type Storage func(t *testing.T) Open

// Open answers a history over that storage.
type Open func() (store.JobHistory, error)

// Run executes the whole contract against an implementation.
func Run(t *testing.T, storage Storage) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, open Open)
	}{
		{"a saved job comes back whole", savedJobComesBackWhole},
		{"a saved job is found by its identifier", savedJobIsFoundByItsIdentifier},
		{"an unknown identifier is not found", unknownIdentifierIsNotFound},
		{"saving twice keeps the last record", savingTwiceKeepsTheLastRecord},
		{"what was saved outlives the store that wrote it", savedHistoryOutlivesTheStore},
		{"the newest job comes first", newestJobComesFirst},
		{"a page stops at its limit and the next one carries on", pagesDoNotOverlapOrSkip},
		{"jobs that ended together are not lost between pages", jobsEndedTogetherSurvivePaging},
		{"a forgotten job is gone", forgottenJobIsGone},
		{"forgetting an unknown job is not found", forgettingUnknownJobIsNotFound},
		{"a job that has not ended is refused", unendedJobIsRefused},
		{"a record with no identifier is refused", recordWithoutIdentifierIsRefused},
		{"a job that ended at no particular time is refused", recordWithoutAnEndIsRefused},
		{"a job that never started keeps its missing beginning", jobThatNeverStartedKeepsNoBeginning},
		{"a cancelled context writes nothing", cancelledContextWritesNothing},
		{"the log is kept, not borrowed", logIsKeptNotBorrowed},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.run(t, storage(t))
		})
	}
}

// The round trip, and the whole of it. Everything a panel draws about a job
// that has ended is in this record, so a field that comes back empty is a row
// that renders wrong — and the two easiest to drop on the way to a database
// are the two that matter most when something went wrong: the error and the
// log.
func savedJobComesBackWhole(t *testing.T, open Open) {
	history := opened(t, open)

	want := store.JobRecord{
		ID:      identifier(t),
		Kind:    "backup",
		Title:   "orders on production",
		State:   job.Failed,
		Err:     "pg_dump exited with status 1",
		Started: at(12, 0, 0),
		Ended:   at(12, 3, 20),
		Log:     []string{"reading the schema", "pg_dump: error: connection failed"},
		Dropped: 7,
	}
	save(t, history, want)

	got := find(t, history, want.ID)
	if got.Kind != want.Kind || got.Title != want.Title {
		t.Errorf("read kind %q and title %q, want %q and %q", got.Kind, got.Title, want.Kind, want.Title)
	}
	if got.State != want.State {
		t.Errorf("read state %v, want %v", got.State, want.State)
	}
	if got.Err != want.Err {
		t.Errorf("read error %q, want %q: a failed job whose reason is gone cannot be acted on", got.Err, want.Err)
	}
	if !got.Started.Equal(want.Started) || !got.Ended.Equal(want.Ended) {
		t.Errorf("read %v to %v, want %v to %v", got.Started, got.Ended, want.Started, want.Ended)
	}
	if got.Dropped != want.Dropped {
		t.Errorf("read %d dropped lines, want %d: a truncated log passed off as whole is read as the whole story",
			got.Dropped, want.Dropped)
	}
	if len(got.Log) != len(want.Log) {
		t.Fatalf("read %d lines of log, want %d: %q", len(got.Log), len(want.Log), got.Log)
	}
	for i, line := range want.Log {
		if got.Log[i] != line {
			t.Errorf("line %d reads %q, want %q", i, got.Log[i], line)
		}
	}
}

// The panel opened on a job that ended last week has an identifier and nothing
// else, and the log it needs is in that one row. Reading the whole history to
// find it would be paging through a year to answer about a line.
func savedJobIsFoundByItsIdentifier(t *testing.T, open Open) {
	history := opened(t, open)

	written := ended(identifier(t))
	written.Log = []string{"what it said"}
	save(t, history, written)

	got, err := history.Get(t.Context(), written.ID)
	if err != nil {
		t.Fatalf("asking for job %s: %v", written.ID, err)
	}
	if got.ID != written.ID {
		t.Errorf("asking for %s answered %s", written.ID, got.ID)
	}
	if len(got.Log) != 1 || got.Log[0] != "what it said" {
		t.Errorf("the job came back with the log %q, want what it said", got.Log)
	}
}

func unknownIdentifierIsNotFound(t *testing.T, open Open) {
	history := opened(t, open)

	if _, err := history.Get(t.Context(), identifier(t)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("asking for an unknown job returned %v, want ErrNotFound", err)
	}
}

// A job is written to the history once, but writing it again must not file a
// second one. It is what makes saving safe to retry, and what stops one job
// from becoming two rows in the panel wearing the same identifier.
func savingTwiceKeepsTheLastRecord(t *testing.T, open Open) {
	history := opened(t, open)

	first := ended(identifier(t))
	first.Title = "before"
	save(t, history, first)

	second := first
	second.Title = "after"
	save(t, history, second)

	if got := all(t, history, first.ID); len(got) != 1 {
		t.Fatalf("the history holds %d records for one job, want 1", len(got))
	}
	if got := find(t, history, first.ID); got.Title != "after" {
		t.Errorf("the record reads %q, want the one saved last", got.Title)
	}
}

// The reason the history exists at all: the panel has to say what happened
// yesterday, and yesterday's process is gone.
func savedHistoryOutlivesTheStore(t *testing.T, open Open) {
	written := ended(identifier(t))
	save(t, opened(t, open), written)

	got := find(t, opened(t, open), written.ID)
	if got.ID != written.ID {
		t.Errorf("the record read back is %q, want %q", got.ID, written.ID)
	}
}

// Newest first, because the job somebody is looking for is almost always the
// one that just finished. The order is part of the contract rather than the
// business of whoever reads: a panel that sorted what it was given would have
// to hold the whole history to do it, which is what paging exists to avoid.
func newestJobComesFirst(t *testing.T, open Open) {
	history := opened(t, open)

	older := ended(identifier(t))
	older.Ended = at(9, 0, 0)
	newer := ended(identifier(t))
	newer.Ended = at(11, 0, 0)

	save(t, history, older)
	save(t, history, newer)

	got := recent(t, history, store.Page{})
	if first := indexOf(got, newer.ID); first > indexOf(got, older.ID) {
		t.Errorf("the job that ended later is at %d, after the one that ended earlier", first)
	}
}

// Paging is keyset rather than offset, so a page asked for after another job
// has ended must not repeat the row the previous page ended on, nor skip the
// one after it. Offsets do exactly that, and a history that grows while
// somebody reads it is the case where they do.
func pagesDoNotOverlapOrSkip(t *testing.T, open Open) {
	history := opened(t, open)

	ids := make([]string, 3)
	for i := range ids {
		record := ended(identifier(t))
		record.Ended = at(10, i, 0)
		ids[i] = record.ID
		save(t, history, record)
	}

	var seen []string
	page := store.Page{Limit: 1}
	for range ids {
		got := recent(t, history, page)
		if len(got) != 1 {
			t.Fatalf("a page of one answered %d records", len(got))
		}
		seen = append(seen, got[0].ID)
		page.After = got[0].Cursor()
	}

	want := []string{ids[2], ids[1], ids[0]}
	for i, id := range want {
		if seen[i] != id {
			t.Fatalf("the pages read %v, want the three jobs newest first: %v", seen, want)
		}
	}
}

// Two jobs can end in the same instant — a queue cancelled wholesale, or a
// clock whose resolution is coarser than the work. A cursor that remembered
// only the time would either repeat every job of that instant for ever or
// skip all but one of them, which is why it carries the identifier too.
func jobsEndedTogetherSurvivePaging(t *testing.T, open Open) {
	history := opened(t, open)

	together := at(10, 0, 0)
	ids := make(map[string]bool, 2)
	for range 2 {
		record := ended(identifier(t))
		record.Ended = together
		ids[record.ID] = true
		save(t, history, record)
	}

	first := recent(t, history, store.Page{Limit: 1})
	if len(first) != 1 {
		t.Fatalf("the first page answered %d records, want 1", len(first))
	}

	second := recent(t, history, store.Page{Limit: 1, After: first[0].Cursor()})
	if len(second) != 1 {
		t.Fatalf("the second page answered %d records, want 1: a job of the same instant was lost", len(second))
	}
	if second[0].ID == first[0].ID {
		t.Errorf("both pages answered %q: the cursor cannot tell two jobs of one instant apart", first[0].ID)
	}
	if !ids[second[0].ID] {
		t.Errorf("the second page answered %q, which is neither of the two jobs saved", second[0].ID)
	}
}

func forgottenJobIsGone(t *testing.T, open Open) {
	history := opened(t, open)

	record := ended(identifier(t))
	save(t, history, record)

	if err := history.Forget(t.Context(), record.ID); err != nil {
		t.Fatalf("forgetting the job: %v", err)
	}

	if got := all(t, history, record.ID); len(got) != 0 {
		t.Errorf("the history still holds %d records for a forgotten job", len(got))
	}
}

// Forgetting something that is not there answers ErrNotFound rather than
// nothing at all. A row somebody dismissed twice is harmless and the caller
// ignores it; an identifier computed wrongly is a bug, and a silent success
// would hide it behind a history that never shrinks.
func forgettingUnknownJobIsNotFound(t *testing.T, open Open) {
	history := opened(t, open)

	if err := history.Forget(t.Context(), identifier(t)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("forgetting an unknown job returned %v, want ErrNotFound", err)
	}
}

// The history is what happened, not what is happening. A running job written
// into it is a row that stays at "running" for ever, because the only thing
// that could ever correct it is the process that has since exited.
func unendedJobIsRefused(t *testing.T, open Open) {
	history := opened(t, open)

	running := ended(identifier(t))
	running.State = job.Running

	if err := history.Save(t.Context(), running); !errors.Is(err, store.ErrNotOver) {
		t.Errorf("saving a running job returned %v, want ErrNotOver", err)
	}
	if got := all(t, history, running.ID); len(got) != 0 {
		t.Errorf("a refused save left %d records behind", len(got))
	}
}

func recordWithoutIdentifierIsRefused(t *testing.T, open Open) {
	history := opened(t, open)

	if err := history.Save(t.Context(), ended("")); !errors.Is(err, store.ErrNoID) {
		t.Errorf("saving a record with no identifier returned %v, want ErrNoID", err)
	}
}

// The end is what the history is ordered by, so a record without one is not a
// row with an empty column: it is a row with no place in the list, which every
// page would either repeat or skip.
func recordWithoutAnEndIsRefused(t *testing.T, open Open) {
	history := opened(t, open)

	endless := ended(identifier(t))
	endless.Ended = time.Time{}

	if err := history.Save(t.Context(), endless); !errors.Is(err, store.ErrNoEnd) {
		t.Errorf("saving a job with no end returned %v, want ErrNoEnd", err)
	}
}

// A job cancelled before it ever ran has no beginning, and that is an outcome
// rather than a missing value: the row is what explains why the backup
// somebody asked for never happened. A store that filled the gap in with the
// end, or with the epoch, would be answering a question nobody asked.
func jobThatNeverStartedKeepsNoBeginning(t *testing.T, open Open) {
	history := opened(t, open)

	never := ended(identifier(t))
	never.State = job.Cancelled
	never.Started = time.Time{}
	save(t, history, never)

	if got := find(t, history, never.ID); !got.Started.IsZero() {
		t.Errorf("the job came back as having started at %v, want no beginning at all", got.Started)
	}
}

// Every call takes a context because a history on disk is a file that can be
// slow to answer. An implementation whose storage is a map has to refuse a
// cancelled context too, or the double would be easier to satisfy than the
// store it stands in for.
func cancelledContextWritesNothing(t *testing.T, open Open) {
	history := opened(t, open)

	record := ended(identifier(t))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := history.Save(ctx, record); !errors.Is(err, context.Canceled) {
		t.Errorf("saving on a cancelled context returned %v, want context.Canceled", err)
	}
	if _, err := history.Recent(ctx, store.Page{}); !errors.Is(err, context.Canceled) {
		t.Errorf("reading on a cancelled context returned %v, want context.Canceled", err)
	}
	if _, err := history.Get(ctx, record.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("asking for a job on a cancelled context returned %v, want context.Canceled", err)
	}
	if err := history.Forget(ctx, record.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("forgetting on a cancelled context returned %v, want context.Canceled", err)
	}

	if got := all(t, history, record.ID); len(got) != 0 {
		t.Errorf("the cancelled save wrote %d records", len(got))
	}
}

// The log crosses the boundary as a slice, and a slice is a window onto
// somebody else's array. A store that kept the caller's array would have its
// history rewritten by whoever reused the buffer — and the caller here is a
// log buffer that is reused by design.
func logIsKeptNotBorrowed(t *testing.T, open Open) {
	history := opened(t, open)

	record := ended(identifier(t))
	record.Log = []string{"first", "second"}
	save(t, history, record)

	record.Log[0] = "rewritten after saving"

	got := find(t, history, record.ID)
	if len(got.Log) == 0 || got.Log[0] != "first" {
		t.Fatalf("the stored log reads %q: the store kept the caller's slice", got.Log)
	}

	got.Log[0] = "rewritten after reading"
	if again := find(t, history, record.ID); again.Log[0] != "first" {
		t.Errorf("the stored log reads %q after a reader wrote to what it was given", again.Log)
	}
}

// ended is a record of a job that finished, carrying everything the contract
// requires and nothing any case is about.
func ended(id string) store.JobRecord {
	return store.JobRecord{
		ID:      id,
		Kind:    "backup",
		Title:   "a database",
		State:   job.Done,
		Started: at(10, 0, 0),
		Ended:   at(10, 1, 0),
	}
}

// at is a time at whole-millisecond resolution, in UTC.
//
// The contract promises the instant back to the millisecond and says nothing
// about the location, which is what a store keeping a count of milliseconds
// since the epoch can honestly offer. Times with nanoseconds in them would be
// this suite extracting a promise it has no business asking for.
func at(hour, minute, second int) time.Time {
	return time.Date(2026, time.September, 12, hour, minute, second, 0, time.UTC)
}

// identifier answers an identifier no other case uses, so that a case reads
// the same against a history that is empty and one that is not.
func identifier(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("%s-%d", t.Name(), nextID())
}

// ids hands out the numbers that keep identifiers apart. A channel rather than
// a counter behind a mutex, because the cases of this suite run in parallel
// with each other and with whatever else the package under test is doing.
var ids = make(chan int, 1)

func init() { ids <- 0 }

func nextID() int {
	last := <-ids
	ids <- last + 1

	return last + 1
}

func opened(t *testing.T, open Open) store.JobHistory {
	t.Helper()

	history, err := open()
	if err != nil {
		t.Fatalf("opening the history: %v", err)
	}

	return history
}

func save(t *testing.T, history store.JobHistory, record store.JobRecord) {
	t.Helper()

	if err := history.Save(t.Context(), record); err != nil {
		t.Fatalf("saving job %s: %v", record.ID, err)
	}
}

func recent(t *testing.T, history store.JobHistory, page store.Page) []store.JobRecord {
	t.Helper()

	got, err := history.Recent(t.Context(), page)
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}

	return got
}

// all answers every record the history holds for one job, walking the pages so
// that a case sees what is there rather than what fitted on the first screen.
func all(t *testing.T, history store.JobHistory, id string) []store.JobRecord {
	t.Helper()

	var found []store.JobRecord

	page := store.Page{}
	for {
		got := recent(t, history, page)
		for _, record := range got {
			if record.ID == id {
				found = append(found, record)
			}
		}

		if len(got) < page.Size() {
			return found
		}
		page.After = got[len(got)-1].Cursor()
	}
}

func find(t *testing.T, history store.JobHistory, id string) store.JobRecord {
	t.Helper()

	got := all(t, history, id)
	if len(got) == 0 {
		t.Fatalf("job %s is not in the history", id)
	}

	return got[0]
}

func indexOf(records []store.JobRecord, id string) int {
	for i, record := range records {
		if record.ID == id {
			return i
		}
	}

	return -1
}
