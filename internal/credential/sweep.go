package credential

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// The name every password file this package writes begins with, followed by the
// process that wrote it and a random tail: pgpass-4242-Xy9.
//
// The process is in the name because it is the only thing that distinguishes a
// file still in use from one somebody's crash left behind, and a second Hermes
// starting up must not delete the file the first one is in the middle of a dump
// with.
const filePrefix = "pgpass-"

// Removing the temporary password file is promised in three layers, because one
// of them cannot cover everything and pretending otherwise would be a lie in a
// place that matters:
//
//  1. Handoff.Release, deferred by the caller. Covers the normal path, the
//     failure path and a cancelled job.
//  2. ReleaseOnInterrupt, below. Covers Ctrl-C and a polite shutdown.
//  3. Sweep, below, run at startup. Covers everything the first two cannot: a
//     kill that runs no code of ours, a panic in the runtime, a power cut.
//
// The third is the one that makes the promise true, and it is the one the tests
// exercise by killing a process outright.

// Guard installs the two layers a whole program is responsible for, and answers
// the function that undoes them.
//
// The sweep runs in the background because it reads a directory, and the
// start-up budget of the window has no room for the disk. Releasing waits for
// it: a program that exits while a sweep of its own is still running would be
// deciding not to know whether it worked.
func (s *Store) Guard(ctx context.Context) (release func()) {
	stop := s.ReleaseOnInterrupt(ctx)

	swept := make(chan struct{})

	go func() {
		defer close(swept)

		s.sweepQuietly()
	}()

	return func() {
		stop()
		<-swept

		if _, err := s.ReleaseAll(); err != nil {
			slog.Warn("removing the password files of this run", "error", err)
		}
	}
}

// sweepQuietly reports what it did to the log rather than to a caller, because
// there is nobody to answer: a failed sweep is worth knowing about and is never
// worth refusing to start over.
func (s *Store) sweepQuietly() {
	removed, err := s.Sweep()

	switch {
	case err != nil:
		slog.Warn("sweeping the password files left by earlier runs", "error", err)
	case removed > 0:
		slog.Info("removed password files left behind by an earlier run", "files", removed)
	}
}

// Sweep removes the password files left behind by processes that are gone.
//
// It is run at startup, in the background, and it answers how many files it
// removed so that a caller can say so. A file belonging to a process that is
// still running is left alone: that is a second Hermes, or this one, in the
// middle of an operation that needs it.
func (s *Store) Sweep() (int, error) {
	return s.remove(func(pid int) bool { return pid != os.Getpid() && !alive(pid) })
}

// ReleaseAll removes the password files this process wrote.
//
// The whole directory rather than a remembered list, because the point of this
// is the paths nobody remembered: a handoff whose Release never ran is exactly
// the file that is still there.
func (s *Store) ReleaseAll() (int, error) {
	return s.remove(func(pid int) bool { return pid == os.Getpid() })
}

// ReleaseOnInterrupt removes the password files of this process when the person
// interrupts it or the system asks it to stop, and then lets the interruption
// do what it was going to do.
//
// The second half of that sentence is the whole difficulty. signal.Notify
// disarms the default disposition for the entire process, so a handler that
// cleans up and returns leaves the program unkillable: the first Ctrl-C runs
// the cleanup and every one after it lands in a channel nobody is reading. The
// handler therefore restores the default and hands the signal back, so the exit
// status and the story told to whoever is waiting are the ones they would have
// been if nothing had been listening.
//
// The returned function stops watching and removes nothing: unregistering is
// not shutting down, and a caller that has finished waiting must not take a
// live file with it.
func (s *Store) ReleaseOnInterrupt(ctx context.Context) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	stopping := make(chan struct{})

	go func() {
		if interrupted := s.releaseOnSignal(ctx, signals, stopping); interrupted != nil {
			reraise(interrupted)
		}
	}()

	var once sync.Once

	return func() {
		once.Do(func() {
			signal.Stop(signals)
			close(stopping)
		})
	}
}

// releaseOnSignal waits, and answers the signal that arrived — nil when the
// watch ended for any other reason.
//
// Answering rather than acting is what lets a test drive it: reraise ends the
// process, and a unit test that called it would take the test binary with it.
// The end-to-end guarantee is asserted by a child process instead.
// The channel is bidirectional because signal.Stop needs to be handed the same
// channel signal.Notify was given, and only this function knows the moment to
// call it.
func (s *Store) releaseOnSignal(ctx context.Context, signals chan os.Signal, stopping <-chan struct{}) os.Signal {
	select {
	case interrupted := <-signals:
		// Restored before the cleanup rather than after it: someone pressing
		// Ctrl-C a second time because the first appeared to do nothing must
		// get the process killed, not swallowed by a handler that is busy.
		signal.Stop(signals)

		if _, err := s.ReleaseAll(); err != nil {
			slog.Warn("removing the password files of this process", "error", err)
		}

		return interrupted
	case <-ctx.Done():
	case <-stopping:
	}

	return nil
}

// remove deletes every password file whose owning process the predicate claims.
//
// A directory that is not there is nothing to sweep rather than a fault: on a
// first run nobody has written a password file yet. A file that has gone
// between the listing and the removal is the outcome asked for — another
// process got there first — and the rest of the sweep carries on either way,
// because one unremovable file must not leave the others behind.
func (s *Store) remove(owned func(pid int) bool) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", s.dir, err)
	}

	removed := 0

	var failures []error

	for _, entry := range entries {
		pid, ours := ownerOf(entry.Name())
		if entry.IsDir() || !ours || !owned(pid) {
			continue
		}

		path := filepath.Join(s.dir, entry.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("removing %s: %w", path, err))

			continue
		}
		removed++
	}

	return removed, errors.Join(failures...)
}

// ownerOf reads the process out of a file name, and reports whether the name is
// one this package wrote at all. Anything else in the directory belongs to
// somebody else and is never touched.
func ownerOf(name string) (int, bool) {
	tail, found := strings.CutPrefix(name, filePrefix)
	if !found {
		return 0, false
	}

	digits, _, found := strings.Cut(tail, "-")
	if !found {
		return 0, false
	}

	// The upper bound is what every system this runs on agrees a process
	// identifier fits in. A name claiming a larger one claims no process, and
	// letting it through would have the sweep asking the operating system about
	// a number it has to truncate to answer.
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 || pid > math.MaxInt32 {
		return 0, false
	}

	return pid, true
}
