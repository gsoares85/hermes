package job

import (
	"context"
	"fmt"
	"time"
)

// How long a cleanup is given before the job is closed without it.
//
// A cleanup removes a partial file and kills a process group, and both of
// those can block on a filesystem or an operating system that is not
// answering. The person pressed Stop: a button that does nothing visible while
// something else waits for ever is the state this package exists to prevent.
const defaultCleanupTimeout = 2 * time.Second

// Cleaner is work that has something to undo when it is cancelled.
//
// Optional, and found by asking the Runner rather than declared beside it,
// because what there is to undo is known by the work and by nothing else: the
// partial file it opened, the process group it started, the backend it left
// running on the server. A Runner that implements nothing here is work with
// nothing to undo, which is most of it.
//
// The context carries the deadline. A cleanup that ignores it is a cleanup the
// queue will stop waiting for, and the job is then reported as failed rather
// than as cleanly cancelled — because the partial file may still be there.
type Cleaner interface {
	Cleanup(ctx context.Context) error
}

// Cancel asks a job to stop.
//
// It returns as soon as the work has been told, never when the work has
// stopped: how long that takes is the work's business, and holding the window
// until a subprocess notices is the thing being cancelled here in the first
// place. Whoever needs the end waits for it.
//
// Cancelling a job that is already stopping, or one that has already ended, is
// not an error. Two people pressing Stop is one cancellation, and a button
// pressed a moment too late should not report a problem.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()

	held, found := q.jobs[id]
	if !found {
		q.mu.Unlock()

		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	// A job that never started has nothing to wind down and nothing to clean
	// up, so it goes straight to the end. Doing it here rather than letting
	// the work notice is what stops a backup somebody already called off from
	// starting at all.
	if held.view.State == Pending {
		held.view.State = Cancelled
		held.view.Ended = q.now()
		close(held.done)
	}

	stop := held.stop
	q.mu.Unlock()

	// Outside the lock: cancelling a context runs whatever is waiting on it,
	// and holding the queue's lock while other people's code runs is how a
	// window stops repainting.
	stop()

	q.moveTo(held, Cancelling)

	return nil
}

// cleanup gives the work a bounded chance to undo what it started.
//
// Answers the error to report, or nil when there was nothing to do or it was
// done. A cleanup that fails, or that runs out of time, is reported: it is the
// partial file nobody removed, and saying the job cancelled cleanly would
// claim it is gone.
func (q *Queue) cleanup(run Runner) error {
	cleaner, has := run.(Cleaner)
	if !has {
		return nil
	}

	ctx, give := context.WithTimeout(context.Background(), q.cleanupTimeout)
	defer give()

	if err := cleaner.Cleanup(ctx); err != nil {
		return fmt.Errorf("cleaning up after the job was cancelled: %w", err)
	}

	return nil
}
