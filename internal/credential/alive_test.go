package credential

import (
	"math"
	"os"
	"testing"
)

// The sweep decides whether to delete a password file by asking this, so both
// of its answers are load-bearing: a wrong "gone" deletes a file an operation
// is using, and a wrong "running" keeps a password on disk for ever.
func TestALiveProcessIsReportedAlive(t *testing.T) {
	t.Parallel()

	if !alive(os.Getpid()) {
		t.Error("alive(this process) = false")
	}
}

// A number that is not a process identifier names no process. Zero and the
// negatives especially: on Unix they address a process group rather than a
// process, and answering "running" for them would make the sweep a no-op.
func TestWhatIsNotAProcessIsNotAlive(t *testing.T) {
	t.Parallel()

	for name, pid := range map[string]int{
		"zero":               0,
		"a negative number":  -1,
		"a whole group":      -4242,
		"beyond every table": math.MaxInt32,
	} {
		if alive(pid) {
			t.Errorf("alive(%s = %d) = true", name, pid)
		}
	}
}
