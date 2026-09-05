package filestore

import "testing"

// A directory that is there is flushed without complaint. Whether the platform
// has anything to flush is the platform's business; what this asserts is that
// the step Save now depends on is not an error Save has to swallow.
func TestFlushingADirectoryThatExistsSucceeds(t *testing.T) {
	t.Parallel()

	if err := syncDir(t.TempDir()); err != nil {
		t.Errorf("syncDir(...) = %v, want nil", err)
	}
}
