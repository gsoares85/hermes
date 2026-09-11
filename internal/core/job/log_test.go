package job_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/gsoares85/hermes/internal/core/job"
)

// logf writes to a job's log. The log is documented to take everything and
// report success, so a failure here is the documentation being wrong rather
// than something a test should tolerate.
func logf(t *testing.T, w job.Log, format string, args ...any) {
	t.Helper()

	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		t.Errorf("writing to the log: %v", err)
	}
}

// logging runs work that writes to the log and answers what the queue kept.
func logging(t *testing.T, queue *job.Queue, write func(w job.Log)) []string {
	t.Helper()

	view := submitted(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		write(report.Log())

		return nil
	}))

	lines, err := queue.Log(view.ID)
	if err != nil {
		t.Fatalf("Log(...) = _, %v, want no error", err)
	}

	return lines
}

func TestWhatWorkWritesIsWhatTheLogKeeps(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	lines := logging(t, queue, func(w job.Log) {
		logf(t, w, "pg_dump: dumping contents of table public.orders\n")
		logf(t, w, "pg_dump: dumping contents of table public.customers\n")
	})

	want := []string{
		"pg_dump: dumping contents of table public.orders",
		"pg_dump: dumping contents of table public.customers",
	}

	if len(lines) != len(want) {
		t.Fatalf("the log has %d lines, want %d: %q", len(lines), len(want), lines)
	}

	for at, line := range want {
		if lines[at] != line {
			t.Errorf("line %d is %q, want %q", at, lines[at], line)
		}
	}
}

// A subprocess writes when its buffer fills, not when a line ends. Half a line
// followed by the rest of it is one line, or the log of every backup is a
// shredded version of what pg_dump actually said.
func TestAWriteThatStopsInTheMiddleOfALine(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	lines := logging(t, queue, func(w job.Log) {
		logf(t, w, "pg_dump: dumping ")
		logf(t, w, "contents of ")
		logf(t, w, "table public.orders\n")
	})

	if len(lines) != 1 || lines[0] != "pg_dump: dumping contents of table public.orders" {
		t.Errorf("the log is %q, want one whole line", lines)
	}
}

// The last thing a process says before it dies is often the most useful, and
// it is exactly the thing with no newline after it.
func TestTheLastLineArrivesWithoutANewline(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	lines := logging(t, queue, func(w job.Log) {
		logf(t, w, "pg_dump: error: connection to server was lost")
	})

	if len(lines) != 1 || lines[0] != "pg_dump: error: connection to server was lost" {
		t.Errorf("the log is %q, want the unterminated line", lines)
	}
}

// Windows subprocesses end lines with two characters, and a carriage return
// kept in the text is a stray glyph in the panel of every backup on Windows.
func TestLinesFromWindowsLoseTheirCarriageReturn(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	lines := logging(t, queue, func(w job.Log) {
		logf(t, w, "pg_restore: creating TABLE public.orders\r\n")
	})

	if len(lines) != 1 || lines[0] != "pg_restore: creating TABLE public.orders" {
		t.Errorf("the log is %q, want the line without its carriage return", lines)
	}
}

// The security test TESTING.md requires, on the surface this task creates.
//
// Nobody writes a password to a log on purpose. It arrives inside the stderr
// of pg_dump and inside the message a driver builds from the connection string
// it was handed, and both of those go through here.
func TestASecretNeverReachesTheLog(t *testing.T) {
	t.Parallel()

	const password = "hunter2-the-real-one"

	cases := []struct {
		name  string
		wrote string
	}{
		{
			"a connection URI in an error",
			`pg_dump: error: connection to server at "db" failed: ` +
				"postgres://hermes:" + password + "@db.example.com:5432/shop",
		},
		{
			"a keyword connection string",
			"connecting with host=db.example.com user=hermes password=" + password,
		},
		// The shape fmt produces with %+v, and therefore the shape the slog
		// handler produces for an attribute it does not otherwise recognise.
		// Plain %v is deliberately absent: it prints values without their field
		// names, so nothing — here or anywhere else — can tell which of the
		// bare strings was the password.
		{
			"a struct printed whole",
			fmt.Sprintf("%+v", struct {
				Host     string
				Password string
			}{"db.example.com", password}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			queue := job.NewQueue()

			lines := logging(t, queue, func(w job.Log) {
				logf(t, w, "%s\n", tc.wrote)
			})

			whole := strings.Join(lines, "\n")
			if strings.Contains(whole, password) {
				t.Errorf("the log kept the password: %q", whole)
			}

			if whole == "" {
				t.Error("the log kept nothing at all, want the line with the password removed")
			}
		})
	}
}

// Redaction happens on the way in, not on the way out. Anything else leaves
// the secret sitting in memory, and puts it in the history on disk when that
// arrives.
func TestTheSecretIsGoneBeforeItIsStored(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	lines := logging(t, queue, func(w job.Log) {
		logf(t, w, "password=%s\n", "hunter2-the-real-one")
	})

	// What comes back is what was kept, so a placeholder in it is the proof
	// that what was kept is not the password.
	if !strings.Contains(strings.Join(lines, "\n"), "xxxxx") {
		t.Errorf("the log is %q, want the password replaced in place", lines)
	}
}

func TestALogStopsGrowingAtItsCeiling(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue(job.WithLogLines(10))

	lines := logging(t, queue, func(w job.Log) {
		for at := range 1000 {
			logf(t, w, "line %d\n", at)
		}
	})

	if len(lines) != 10 {
		t.Fatalf("the log has %d lines, want 10", len(lines))
	}

	// What is dropped is the beginning. The end of a log is where the failure
	// is, and a log truncated from the wrong end keeps a thousand lines of
	// nothing happening.
	if lines[len(lines)-1] != "line 999" {
		t.Errorf("the last line is %q, want %q", lines[len(lines)-1], "line 999")
	}

	if lines[0] != "line 990" {
		t.Errorf("the first line is %q, want %q", lines[0], "line 990")
	}
}

// A log truncated in silence is worse than a short log: somebody reads the
// first line kept as the beginning of the operation.
func TestTheLogSaysWhenItHasDroppedSomething(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue(job.WithLogLines(10))

	view := submitted(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		for at := range 25 {
			logf(t, report.Log(), "line %d\n", at)
		}

		return nil
	}))

	if view.Dropped != 15 {
		t.Errorf("the job dropped %d lines, want 15", view.Dropped)
	}
}

// One enormous line is the other way to fill memory, and a ceiling counted in
// lines does not see it coming.
func TestALineLongerThanTheLogWillHold(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue(job.WithLogBytes(100))

	lines := logging(t, queue, func(w job.Log) {
		logf(t, w, "%s\n", strings.Repeat("x", 10_000))
	})

	whole := strings.Join(lines, "\n")
	if len(whole) > 100 {
		t.Errorf("the log holds %d bytes, want at most 100", len(whole))
	}

	if whole == "" {
		t.Error("the log kept nothing, want as much of the line as fits")
	}
}

// The corpus this product is tested against is full of names with accents, and
// a cut through the middle of one is invalid UTF-8 — which does not survive
// the trip to the window as JSON.
func TestALongLineIsCutBetweenCharacters(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue(job.WithLogBytes(51))

	lines := logging(t, queue, func(w job.Log) {
		// Two bytes each, so a cut at an odd offset lands inside one.
		logf(t, w, "%s\n", strings.Repeat("é", 100))
	})

	whole := strings.Join(lines, "\n")
	if !utf8.ValidString(whole) {
		t.Errorf("the log holds invalid UTF-8: %q", whole)
	}

	if len(whole) > 51 {
		t.Errorf("the log holds %d bytes, want at most 51", len(whole))
	}
}

// A log that never stops writing is a log whose job ended and whose
// subprocess did not. What it says after the end is not part of the job.
func TestWritingAfterTheJobIsOverChangesNothing(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	var late job.Log
	view := submitted(t, queue, runnerFunc(func(_ context.Context, report job.Reporter) error {
		late = report.Log()
		logf(t, late, "the last thing it said\n")

		return nil
	}))

	logf(t, late, "after the end\n")

	lines, err := queue.Log(view.ID)
	if err != nil {
		t.Fatalf("Log(...) = _, %v, want no error", err)
	}

	if len(lines) != 1 || lines[0] != "the last thing it said" {
		t.Errorf("the log is %q, want only what was written before the end", lines)
	}
}

func TestAskingForTheLogOfAJobTheQueueDoesNotHave(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	if _, err := queue.Log("no-such-job"); err == nil {
		t.Error("Log(...) = _, nil, want an error")
	}
}

// A subprocess writes from a goroutine of its own while the panel reads. Under
// -race this is what says the buffer is not shared unguarded.
func TestWritingAndReadingTheLogAtTheSameTime(t *testing.T) {
	t.Parallel()

	queue := job.NewQueue()

	release := make(chan struct{})
	id, err := queue.Submit(spec("a job"), runnerFunc(func(_ context.Context, report job.Reporter) error {
		var writers sync.WaitGroup
		for writer := range 4 {
			writers.Go(func() {
				for at := range 250 {
					logf(t, report.Log(), "writer %d line %d\n", writer, at)
				}
			})
		}

		writers.Wait()
		close(release)

		return nil
	}))
	if err != nil {
		t.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 250 {
				if _, err := queue.Log(id); err != nil {
					t.Errorf("Log(...) = _, %v, want no error", err)

					return
				}
			}
		})
	}

	readers.Wait()
	<-release
}
