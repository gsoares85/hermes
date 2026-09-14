package job

import (
	"errors"
	"fmt"
)

// State is where a job is in its life.
//
// A job is a thing somebody is watching happen, and the states are the ones
// they can tell apart on screen: waiting, working, stopping, and three ways of
// being over. Cancelling is a state of its own rather than a flag, because it
// is the only honest answer to "did it stop?" while the work has been asked to
// stop and has not stopped yet — a backup whose pg_dump is still writing has
// not stopped, and saying it has is how a half-written file gets treated as no
// file at all.
type State int

// The states, in the order a job meets them. Pending is the zero value because
// it is where a job is before anything has happened to it; any other choice
// would make the useful state the one somebody has to remember to set.
const (
	Pending State = iota
	Running
	Cancelling
	Done
	Failed
	Cancelled
)

// ErrNotAState is a name that stands for no state.
//
// It exists because the names leave the process: they are what the history on
// disk keeps and what the window reads. Something has to say what a name that
// came back and belongs to nothing means, and the answer is a refusal rather
// than a plausible state — a row read as "pending" because its state could not
// be understood is a job the panel shows as about to start.
var ErrNotAState = errors.New("not the name of a state a job can be in")

// ErrNotATransition is a move a job cannot make.
//
// One sentinel rather than one per illegal pair: the caller has nothing
// different to do about a job that was asked to go backwards and one that was
// asked to finish twice. Both are bugs in whoever asked, and the message says
// which.
var ErrNotATransition = errors.New("not a transition a job can make")

// transitions is the machine, written out.
//
// A table rather than a switch, because the question asked of it most often is
// not "what happens next" but "can this happen at all", and a table answers
// that by looking rather than by reading. What is absent is as deliberate as
// what is present: there is no way back to Pending, no way out of an end, and
// no way from Running straight to Cancelled without passing through the state
// that says the work is still winding down.
var transitions = map[State][]State{
	// Cancelled directly: nothing has been started, so there is nothing to
	// wind down and nothing to clean up.
	Pending:    {Running, Cancelled},
	Running:    {Done, Failed, Cancelling},
	Cancelling: {Cancelled, Done, Failed},
	// The three ends are absent on purpose. A job that moves after the person
	// watched it finish is a job that reappears in the panel, and running one
	// again is a new job with a new identifier rather than this one resumed.
	Done:      nil,
	Failed:    nil,
	Cancelled: nil,
}

// Transitioned answers the state a job is in after moving, or why it cannot.
//
// It answers the destination rather than mutating anything, so that the rule
// stays usable from inside a lock without holding one, and testable without a
// job to apply it to.
func Transitioned(from, to State) (State, error) {
	for _, allowed := range transitions[from] {
		if allowed == to {
			return to, nil
		}
	}

	return from, fmt.Errorf("%w: %v to %v", ErrNotATransition, from, to)
}

// Over reports whether the job has reached an end it cannot leave.
//
// Asked by everything that has to decide between showing progress and showing
// a result, so it is a question about the state rather than a list of three
// states repeated at every call site.
func (s State) Over() bool {
	return len(transitions[s]) == 0 && s.named()
}

// String is the name that crosses the boundary to the window and goes into the
// history on disk. It is part of the contract, not a debugging convenience:
// renaming one is a migration.
func (s State) String() string {
	if !s.named() {
		return "unknown"
	}

	return names[s]
}

// ParseState answers the state a name stands for.
//
// The other half of String, and here beside it so that the two cannot drift:
// whatever crosses to the history has to be able to come back, and a name
// added to one map and forgotten in the other is a row that stops being
// readable the moment it is written.
func ParseState(name string) (State, error) {
	for state, spelling := range names {
		if spelling == name {
			return state, nil
		}
	}

	return Pending, fmt.Errorf("%w: %q", ErrNotAState, name)
}

var names = map[State]string{
	Pending:    "pending",
	Running:    "running",
	Cancelling: "cancelling",
	Done:       "done",
	Failed:     "failed",
	Cancelled:  "cancelled",
}

// named separates the six states from an integer that happens to be a State.
// A value from outside the set is a bug somewhere else, and it must not be
// able to hide behind a plausible name or a plausible answer to Over.
func (s State) named() bool {
	_, ok := names[s]

	return ok
}
