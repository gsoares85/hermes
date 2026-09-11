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
	mu       sync.Mutex
	lines    []string
	partial  []byte
	bytes    int
	dropped  int
	maxLines int
	maxBytes int
	closed   bool
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
		l.dropped++
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

	l.dropped++

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

// droppedCount answers how many lines the ceiling took.
func (l *logbook) droppedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.dropped
}
