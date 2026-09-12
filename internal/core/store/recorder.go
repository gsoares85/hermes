package store

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
)

// How long writing one row is given.
//
// Longer than the wait a busy file imposes, so that a write held up by another
// window of Hermes finishing its own row is waited for rather than abandoned;
// short enough that a disk which has stopped answering does not hold the
// goroutine of a job that has already ended.
const recordTimeout = 10 * time.Second

// LogOf answers what a job said, as whoever ran it kept it.
//
// A function rather than the queue itself, because what the recorder needs is
// one answer about one job: handed the queue, it could cancel jobs and submit
// work, and none of that is its business.
type LogOf func(id string) ([]string, error)

// Recorder writes down the jobs that reach an end.
//
// It is shaped to be the queue's observer, which is what makes the history a
// consequence of a job ending rather than something every caller has to
// remember to do. Nothing reads it back: that is the panel's business, through
// the same port.
type Recorder struct {
	history JobHistory
	logOf   LogOf
	// failed is told when a job could not be written down. Injected because
	// what to do about it belongs to whoever built the application — a line in
	// the window's log, a line on stderr — and because a history that quietly
	// stopped being kept is exactly the failure this is here to make visible.
	failed func(error)
}

// NewRecorder creates the recorder.
func NewRecorder(history JobHistory, logOf LogOf, failed func(error)) *Recorder {
	return &Recorder{history: history, logOf: logOf, failed: failed}
}

// Observe files a job that has ended, and ignores one that has not.
//
// Only the end. A job is written once, when there is nothing left to happen to
// it: writing every transition would put a row in the file for a backup that
// is still running, and the only process that could ever correct it is the one
// that might not come back.
//
// It answers nothing, because an observer cannot. A history that could not be
// written is reported through failed and the job is unaffected: somebody's
// backup did not fail because the bookkeeping did.
func (r *Recorder) Observe(view job.View) {
	if !view.State.Over() {
		return
	}

	ctx, give := context.WithTimeout(context.Background(), recordTimeout)
	defer give()

	log, err := r.logOf(view.ID)
	if err != nil {
		// The job was forgotten between ending and being written down. There
		// is no log to keep and no row to write: somebody dismissed it.
		return
	}

	if err := r.history.Save(ctx, RecordOf(view, log)); err != nil {
		r.failed(fmt.Errorf("remembering job %s: %w", view.ID, err))
	}
}

// RecordOf is a job that has ended, as the history keeps it.
//
// The translation lives here rather than in the queue because the queue must
// not know there is a history, and rather than in the recorder because it is
// worth asking about on its own: this is the line where what the window shows
// becomes what tomorrow remembers.
func RecordOf(view job.View, log []string) JobRecord {
	return JobRecord{
		ID:      view.ID,
		Kind:    view.Kind,
		Title:   view.Title,
		State:   view.State,
		Err:     view.Err,
		Started: view.Started,
		Ended:   view.Ended,
		Log:     log,
		Dropped: view.Dropped,
	}
}
