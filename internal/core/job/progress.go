package job

import "time"

// Progress is how far along work says it is.
//
// Total zero means the work does not know how much there is — pg_dump on a
// table whose row count nobody asked for. That is a normal answer, not a
// missing one, and it is the difference between a bar that fills and a bar
// that only moves.
//
// Reporting is meant to be cheap enough to do per row: it is one write of a
// small struct under a lock the readers hold only to copy. There is no queue
// behind it and nothing accumulates, because the only question anybody asks of
// progress is where the work is now.
type Progress struct {
	// Step is what the work is doing, in the words the person reads:
	// "copying public.orders", not "phase 3 of 7".
	Step string
	// Unit is what Done and Total count — "rows", "bytes", "tables".
	Unit string
	// Done is how much of it is finished, and Total how much there is.
	Done  int64
	Total int64
}

// Reporter is how work says how far along it is.
//
// Handed to the Runner rather than fetched by it, so that work is testable
// with a reporter that records, and so that a Runner cannot reach a job it was
// not given.
type Reporter interface {
	Report(Progress)
}

// ProgressView is progress as everything outside this package reads it: what
// the work said, plus what can be worked out from it and the clock.
//
// The derived fields are computed here rather than in the window because they
// are the same arithmetic for every caller, and because getting them wrong is
// how "Infinityms left" reaches a screen.
type ProgressView struct {
	Step  string
	Unit  string
	Done  int64
	Total int64

	// Fraction is between 0 and 1, and is 0 when nothing can be worked out.
	// Never a NaN and never an infinity: both render, and neither means
	// anything to whoever reads them.
	Fraction float64
	// Indeterminate means how far along this is cannot be known — not that it
	// is at nought. A bar sitting at zero says the work has failed to advance,
	// which is a different and worse thing to tell somebody.
	Indeterminate bool
	// Elapsed is how long the job has been going, and stops when it ends.
	Elapsed time.Duration
	// Remaining is the estimate, and is zero whenever Indeterminate is true.
	Remaining time.Duration
}

// viewOf works out what can be said about progress after the given time.
func viewOf(said Progress, elapsed time.Duration) ProgressView {
	view := ProgressView{
		Step:          said.Step,
		Unit:          said.Unit,
		Done:          said.Done,
		Total:         said.Total,
		Indeterminate: true,
		Elapsed:       elapsed,
	}

	// Everything below divides by one of these. A total nobody knows, a count
	// that has not started, and the negatives work should never send all end
	// here, because the alternative is an infinity or a NaN on screen.
	if said.Total <= 0 || said.Done <= 0 {
		return view
	}

	view.Indeterminate = false

	// A server that over-reports must not produce a bar past its end:
	// pg_restore counts what it did, and an estimate of what it would do
	// disagrees.
	if said.Done >= said.Total {
		view.Fraction = 1

		return view
	}

	view.Fraction = float64(said.Done) / float64(said.Total)
	view.Remaining = time.Duration(float64(elapsed) * (1 - view.Fraction) / view.Fraction)

	return view
}
