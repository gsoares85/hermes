package ui

// RememberedForTest counts what the watcher is holding on to about jobs.
//
// Exported to the test binary alone, because what leaks here leaks silently:
// nothing on screen changes, nothing fails, and the maps grow for as long as
// the window is open. The only way to see it is to count.
func (s *JobWatcher) RememberedForTest() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.said) + len(s.lines) + len(s.settled)
}
