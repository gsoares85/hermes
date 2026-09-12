package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
)

// The history answers the contract, checked where it is written: a method
// renamed on the port should stop this file compiling rather than a test
// somewhere else.
var _ store.JobHistory = (*JobHistory)(nil)

// JobHistory is the job history kept in the file.
//
// It holds the handle rather than the Store so that nothing here can close a
// database it did not open: the file is opened and closed by whoever wired the
// application together, and this only ever reads and writes rows.
type JobHistory struct {
	db   *sql.DB
	path string
}

const columns = "id, kind, title, state, started_at, ended_at, error, log, dropped"

// Replacing rather than inserting, because saving the same job twice is a
// retry and not a second job. SQLite writes the whole row either way.
const saveStatement = `INSERT OR REPLACE INTO jobs (` + columns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

const getStatement = `SELECT ` + columns + ` FROM jobs WHERE id = ?`

// The order the contract states, restated in SQL: the end descending, the
// identifier deciding between two that ended together. The index is on the
// same pair, so a page is read from it rather than by sorting the history.
const recentStatement = `SELECT ` + columns + ` FROM jobs
	ORDER BY ended_at DESC, id DESC
	LIMIT ?`

// The same, from where a page stopped. It is store.Cursor.Older written as a
// predicate SQLite can use the index for.
const recentAfterStatement = `SELECT ` + columns + ` FROM jobs
	WHERE ended_at < ? OR (ended_at = ? AND id < ?)
	ORDER BY ended_at DESC, id DESC
	LIMIT ?`

// Save writes the job down, replacing whatever was under its identifier.
func (h *JobHistory) Save(ctx context.Context, record store.JobRecord) error {
	if err := store.Usable(ctx); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}

	_, err := h.db.ExecContext(ctx, saveStatement,
		record.ID,
		record.Kind,
		record.Title,
		record.State.String(),
		instant(record.Started),
		record.Ended.UnixMilli(),
		record.Err,
		joined(record.Log),
		record.Dropped,
	)
	if err != nil {
		return fmt.Errorf("writing job %s to %s: %w", record.ID, h.path, err)
	}

	return nil
}

// Get answers one job, or ErrNotFound.
func (h *JobHistory) Get(ctx context.Context, id string) (store.JobRecord, error) {
	if err := store.Usable(ctx); err != nil {
		return store.JobRecord{}, err
	}

	rows, err := h.db.QueryContext(ctx, getStatement, id)
	if err != nil {
		return store.JobRecord{}, fmt.Errorf("reading job %s in %s: %w", id, h.path, err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if failed := rows.Err(); failed != nil {
			return store.JobRecord{}, fmt.Errorf("reading job %s in %s: %w", id, h.path, failed)
		}

		return store.JobRecord{}, fmt.Errorf("%w: %s", store.ErrNotFound, id)
	}

	record, err := scan(rows)
	if err != nil {
		return store.JobRecord{}, fmt.Errorf("reading job %s in %s: %w", id, h.path, err)
	}

	return record, nil
}

// Recent answers one page of the history, newest first.
func (h *JobHistory) Recent(ctx context.Context, page store.Page) ([]store.JobRecord, error) {
	if err := store.Usable(ctx); err != nil {
		return nil, err
	}

	rows, err := h.query(ctx, page)
	if err != nil {
		return nil, fmt.Errorf("reading the job history in %s: %w", h.path, err)
	}
	defer func() { _ = rows.Close() }()

	found := make([]store.JobRecord, 0, page.Size())
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("reading the job history in %s: %w", h.path, err)
		}

		found = append(found, record)
	}

	// Asked after the loop and not instead of it: a read that failed half way
	// answers rows already scanned and no error, and a history that silently
	// stops at the row a broken file reaches is the panel saying a backup was
	// never run.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the job history in %s: %w", h.path, err)
	}

	return found, nil
}

func (h *JobHistory) query(ctx context.Context, page store.Page) (*sql.Rows, error) {
	if page.After.IsZero() {
		rows, err := h.db.QueryContext(ctx, recentStatement, page.Size())
		if err != nil {
			return nil, fmt.Errorf("asking %s for the newest jobs: %w", h.path, err)
		}

		return rows, nil
	}

	ended := page.After.Ended.UnixMilli()

	rows, err := h.db.QueryContext(ctx, recentAfterStatement, ended, ended, page.After.ID, page.Size())
	if err != nil {
		return nil, fmt.Errorf("asking %s for the jobs before %s: %w", h.path, page.After.ID, err)
	}

	return rows, nil
}

// Forget removes a job from the history.
func (h *JobHistory) Forget(ctx context.Context, id string) error {
	if err := store.Usable(ctx); err != nil {
		return err
	}

	result, err := h.db.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("forgetting job %s in %s: %w", id, h.path, err)
	}

	gone, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("forgetting job %s in %s: %w", id, h.path, err)
	}
	if gone == 0 {
		return fmt.Errorf("%w: %s", store.ErrNotFound, id)
	}

	return nil
}

func scan(rows *sql.Rows) (store.JobRecord, error) {
	var (
		record  store.JobRecord
		state   string
		started sql.NullInt64
		ended   int64
		log     string
	)

	err := rows.Scan(&record.ID, &record.Kind, &record.Title, &state,
		&started, &ended, &record.Err, &log, &record.Dropped)
	if err != nil {
		return store.JobRecord{}, fmt.Errorf("reading a row of the history: %w", err)
	}

	// A name that stands for no state is refused rather than read as the first
	// one: a row shown as pending because nobody could read it is a job the
	// panel says is about to start.
	record.State, err = job.ParseState(state)
	if err != nil {
		return store.JobRecord{}, fmt.Errorf("job %s: %w", record.ID, err)
	}

	record.Started = when(started)
	record.Ended = time.UnixMilli(ended).UTC()
	record.Log = lines(log)

	return record, nil
}

// instant is a time as the file keeps it: milliseconds since the epoch, and
// nothing at all for a job that never started.
//
// Milliseconds rather than nanoseconds because a count of nanoseconds since
// 1970 is halfway through the range of a signed 64-bit integer, and rather
// than seconds because two jobs submitted in the same second are ordinary —
// the cursor can tell them apart by identifier, but a list where the order of
// the afternoon is arbitrary is a list nobody trusts.
func instant(at time.Time) sql.NullInt64 {
	if at.IsZero() {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: at.UnixMilli(), Valid: true}
}

// when is the other half of instant. The zero time comes back as the zero
// time, which is what a job cancelled before it started has for a beginning.
func when(at sql.NullInt64) time.Time {
	if !at.Valid {
		return time.Time{}
	}

	return time.UnixMilli(at.Int64).UTC()
}

// joined is the log as one string: every line with its newline after it.
//
// The newline goes after rather than between so that the encoding is exact at
// both ends. Between, a log of no lines and a log of one empty line are both
// the empty string, and one of them comes back as the other.
//
// A line never contains a newline — the queue's log splits what a subprocess
// writes on exactly that — so nothing here has to escape anything.
func joined(log []string) string {
	var text strings.Builder
	for _, line := range log {
		text.WriteString(line)
		text.WriteByte('\n')
	}

	return text.String()
}

func lines(text string) []string {
	if text == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
