package job

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// What a log holds before it starts dropping the beginning.
//
// Two ceilings because there are two ways to fill memory and one of them does
// not see the other coming: ten thousand lines of pg_restore --verbose, and a
// single line of ten megabytes from a server that answered with a table. The
// budget is 200MB in the whole application with three connections open, and a
// log is the one thing here whose size a person on the other end of a
// subprocess decides.
const (
	defaultLogLines = 5_000
	defaultLogBytes = 1 << 20
)

// Log is where work writes what it is doing.
//
// An io.Writer because that is what a subprocess's stdout is plugged into and
// what fmt.Fprintf takes, so nothing has to be adapted to use it.
type Log interface {
	io.Writer
}

// logbook is the log of one job: whole lines, capped, with what was dropped
// counted.
//
// Lines are assembled here rather than left to the writer because a subprocess
// writes when its buffer fills, not when a line ends. Half a line followed by
// the rest of it is one line, and treating each write as one would shred the
// log of every backup into the shape of the pipe rather than of what pg_dump
// said.
type logbook struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
	bytes   int
	// discarded is how many lines have left by the front, and it is also the
	// sequence of the line now at the front: the two are the same number
	// because nothing is ever removed from anywhere else. It is what lets a
	// reader ask for what is new without holding a place that shifts under it.
	discarded int
	// truncated is how many lines were cut for being longer than the whole
	// log. Counted apart from discarded because a cut line is still in the
	// buffer: adding it to the sequence would skip a line that is there.
	truncated int
	maxLines  int
	maxBytes  int
	closed    bool
}

func newLogbook(maxLines, maxBytes int) *logbook {
	return &logbook{maxLines: maxLines, maxBytes: maxBytes}
}

// Write takes whatever arrived and keeps the whole lines in it.
//
// It never reports a short write or an error: a log that refuses what a
// subprocess says would make the subprocess fail for want of somewhere to
// write, and losing the end of a log is better than losing the backup.
func (l *logbook) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return len(p), nil
	}

	l.partial = append(l.partial, p...)

	for {
		at := bytes.IndexByte(l.partial, '\n')
		if at < 0 {
			break
		}

		l.keep(string(l.partial[:at]))
		l.partial = l.partial[at+1:]
	}

	// A line longer than the whole log would otherwise sit in partial growing
	// for ever, since nothing trims what has not ended yet.
	if len(l.partial) > l.maxBytes {
		l.keep(string(l.partial[:l.maxBytes]))
		l.partial = nil
	}

	return len(p), nil
}

// keep files one line, after taking the secrets out of it and before anything
// else sees it.
//
// On the way in rather than on the way out, because anything else leaves the
// password sitting in memory for as long as the history does — and puts it on
// disk when the history is written. Nobody writes a password to a log on
// purpose; it arrives inside the stderr of pg_dump and inside the message a
// driver builds from the connection string it was handed.
func (l *logbook) keep(line string) {
	line = l.trimmed(secret.Redact(strings.TrimSuffix(line, "\r")))

	l.lines = append(l.lines, line)
	l.bytes += len(line)

	// The beginning is what goes. The end of a log is where the failure is,
	// and a log trimmed from the wrong end keeps a thousand lines of nothing
	// happening and drops the line that says why it stopped.
	for len(l.lines) > l.maxLines || (l.bytes > l.maxBytes && len(l.lines) > 1) {
		l.bytes -= len(l.lines[0])
		l.lines = l.lines[1:]
		l.discarded++
	}
}

// trimmed cuts a line that is longer than the whole log down to what the log
// will hold.
//
// Without it one enormous line escapes both ceilings: the count of lines does
// not see it, and the count of bytes cannot drop it, because dropping the only
// line would leave the log empty and still over budget. A server that answers
// with a table inside an error message is where such a line comes from.
//
// The cut lands on a rune boundary. A name with an accent split down the
// middle is invalid UTF-8, which does not survive the trip to the window as
// JSON — and the corpus this product is tested against is full of them.
func (l *logbook) trimmed(line string) string {
	if len(line) <= l.maxBytes {
		return line
	}

	cut := l.maxBytes
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}

	l.truncated++

	return line[:cut]
}

// close keeps whatever was written without a newline after it.
//
// The last thing a process says before it dies is often the most useful, and
// it is exactly the thing with no newline after it.
func (l *logbook) close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	if len(l.partial) > 0 {
		l.keep(string(l.partial))
		l.partial = nil
	}

	l.closed = true
}

// read answers a copy, so that what a caller is reading cannot change under it
// while a subprocess keeps writing.
func (l *logbook) read() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	lines := make([]string, len(l.lines))
	copy(lines, l.lines)

	return lines
}

// droppedCount answers how much the ceiling took: lines that left by the
// front, and lines that were cut for being longer than the whole log.
func (l *logbook) droppedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.discarded + l.truncated
}

// since answers the lines filed after a sequence, and the sequence to ask with
// next time.
//
// A sequence rather than a count of what is held, because what is held stops
// growing the moment the log is full: from then on a line arrives at the back
// for every line that leaves the front, and a reader holding a length would
// see the same number for ever and conclude that nothing had been written. The
// sequence counts what has been filed since the log began, which only ever
// goes up.
//
// A reader that fell behind the front is given what survives and no warning:
// what it missed is the same thing droppedCount reports, and the panel already
// says so above the log.
func (l *logbook) since(seq int) ([]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	first := l.discarded
	next := first + len(l.lines)

	seq = min(max(seq, first), next)

	fresh := make([]string, next-seq)
	copy(fresh, l.lines[seq-first:])

	return fresh, next
}
