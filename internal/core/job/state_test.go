package job_test

import (
	"errors"
	"testing"

	"github.com/gsoares85/hermes/internal/core/job"
)

// Every state a job can be in, so that a state added later without a rule about
// it fails a test rather than travelling silently.
var everyState = []job.State{
	job.Pending,
	job.Running,
	job.Cancelling,
	job.Done,
	job.Failed,
	job.Cancelled,
}

func TestAJobStartsPending(t *testing.T) {
	t.Parallel()

	// The zero value is the state a job is in before anything has happened to
	// it. Any other choice makes the useful state the one somebody has to
	// remember to set.
	var state job.State

	if state != job.Pending {
		t.Errorf("the zero State is %v, want %v", state, job.Pending)
	}
}

func TestTheStatesAJobMovesThrough(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from job.State
		to   job.State
	}{
		{"picked up from the queue", job.Pending, job.Running},
		{"finished", job.Running, job.Done},
		{"failed", job.Running, job.Failed},
		// Asked for while it was still waiting. Nothing is running, so there
		// is nothing to wind down and it goes straight to the end.
		{"cancelled before it began", job.Pending, job.Cancelled},
		// Asked for while it was working. The work has been told to stop and
		// has not stopped yet, which is the state the person sees.
		{"asked to stop", job.Running, job.Cancelling},
		{"stopped", job.Cancelling, job.Cancelled},
		// The work reached its end before it noticed it had been asked to
		// stop. Cancelling is a request, not a promise: a job that finishes in
		// the gap finished, and saying otherwise would report a backup that
		// exists as one that does not.
		{"finished while stopping", job.Cancelling, job.Done},
		// The same race, the other way. Cleanup that fails during cancellation
		// is a failure worth reporting: it is the partial file nobody removed.
		{"failed while stopping", job.Cancelling, job.Failed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := job.Transitioned(tc.from, tc.to)
			if err != nil {
				t.Fatalf("Transitioned(%v, %v) = _, %v, want no error", tc.from, tc.to, err)
			}

			if got != tc.to {
				t.Errorf("Transitioned(%v, %v) = %v, want %v", tc.from, tc.to, got, tc.to)
			}
		})
	}
}

func TestAFinishedJobNeverMovesAgain(t *testing.T) {
	t.Parallel()

	// The three ends of a job. A transition out of one of them is a job that
	// reappears in the panel after the person watched it finish, and it is the
	// shape a retry would take if retrying were ever confused with resuming.
	for _, from := range []job.State{job.Done, job.Failed, job.Cancelled} {
		for _, to := range everyState {
			t.Run(from.String()+" to "+to.String(), func(t *testing.T) {
				t.Parallel()

				if _, err := job.Transitioned(from, to); !errors.Is(err, job.ErrNotATransition) {
					t.Errorf(
						"Transitioned(%v, %v) = _, %v, want %v",
						from, to, err, job.ErrNotATransition,
					)
				}
			})
		}
	}
}

func TestTheMovesAJobCannotMake(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from job.State
		to   job.State
	}{
		// Every state is somewhere a job has been. Going back to it is not a
		// transition, it is a second job.
		{"back to the queue", job.Running, job.Pending},
		{"back to work", job.Cancelling, job.Running},
		// Cancelling is how a running job stops. Skipping it would lose the
		// only state in which the window can say "stopping…" rather than
		// claiming a job stopped while its subprocess is still writing.
		{"stopping without being asked to stop", job.Running, job.Cancelled},
		// A job that was never picked up cannot have produced anything.
		{"finishing without running", job.Pending, job.Done},
		{"failing without running", job.Pending, job.Failed},
		{"stopping without running", job.Pending, job.Cancelling},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := job.Transitioned(tc.from, tc.to); !errors.Is(err, job.ErrNotATransition) {
				t.Errorf(
					"Transitioned(%v, %v) = _, %v, want %v",
					tc.from, tc.to, err, job.ErrNotATransition,
				)
			}
		})
	}
}

// A job that is told to enter the state it is already in has not moved, and
// nothing about it should change. It is asked for by two people pressing Cancel
// on the same job, and by a retry of a call that already landed.
func TestStayingPutIsNotATransition(t *testing.T) {
	t.Parallel()

	for _, state := range everyState {
		t.Run(state.String(), func(t *testing.T) {
			t.Parallel()

			if _, err := job.Transitioned(state, state); !errors.Is(err, job.ErrNotATransition) {
				t.Errorf(
					"Transitioned(%v, %v) = _, %v, want %v",
					state, state, err, job.ErrNotATransition,
				)
			}
		})
	}
}

func TestWhetherAJobIsOver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state job.State
		over  bool
	}{
		{job.Pending, false},
		{job.Running, false},
		{job.Cancelling, false},
		{job.Done, true},
		{job.Failed, true},
		{job.Cancelled, true},
	}

	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			t.Parallel()

			if got := tc.state.Over(); got != tc.over {
				t.Errorf("%v.Over() = %v, want %v", tc.state, got, tc.over)
			}
		})
	}
}

// The names cross the boundary to the window and go into the history on disk,
// so they are part of the contract rather than a debugging convenience. A
// rename is a migration, and this is what makes that visible.
func TestEveryStateSaysWhatItIs(t *testing.T) {
	t.Parallel()

	want := map[job.State]string{
		job.Pending:    "pending",
		job.Running:    "running",
		job.Cancelling: "cancelling",
		job.Done:       "done",
		job.Failed:     "failed",
		job.Cancelled:  "cancelled",
	}

	for state, name := range want {
		if got := state.String(); got != name {
			t.Errorf("State(%d).String() = %q, want %q", int(state), got, name)
		}
	}

	// A State that is not one of the six is a bug somewhere else, and it must
	// not be able to hide behind a plausible name.
	if got := job.State(len(everyState) + 1).String(); got != "unknown" {
		t.Errorf("an unnamed State says %q, want %q", got, "unknown")
	}
}

// Every state survives the trip to the history and back. The names are what a
// row on disk holds, so a state added to one map and forgotten in the other is
// a job that stops being readable the moment it is written.
func TestEveryStateSurvivesItsName(t *testing.T) {
	t.Parallel()

	for _, state := range everyState {
		got, err := job.ParseState(state.String())
		if err != nil {
			t.Errorf("reading %q back: %v", state.String(), err)

			continue
		}
		if got != state {
			t.Errorf("%q reads back as %v, want %v", state.String(), got, state)
		}
	}
}

// A name that belongs to nothing is refused rather than read as the first
// state: a row shown as pending because nobody could read it is a job the
// panel says is about to start.
func TestANameThatIsNoStateIsRefused(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "finito", "Done", "unknown"} {
		if _, err := job.ParseState(name); !errors.Is(err, job.ErrNotAState) {
			t.Errorf("reading %q returned %v, want ErrNotAState", name, err)
		}
	}
}
