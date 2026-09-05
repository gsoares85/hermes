package credential

import (
	"errors"
	"os"
	"testing"
)

// counting is a claim that records how often it was asked to let go.
type counting struct {
	closed int
	err    error
}

func (c *counting) Close() error {
	c.closed++

	return c.err
}

// The assertion nothing made before, and the one the defect hid behind: the
// operating system is asked to give the claim up at most once, however many
// times the handoff is released.
//
// The copy is not incidental. Handoff is a value with a value receiver, so a
// deferred Release after an explicit one runs against a different copy of the
// struct, and neither copy can tell that the other has already let go.
func TestTheClaimIsGivenUpAtMostOnce(t *testing.T) {
	t.Parallel()

	claim := &counting{}
	handoff := Handoff{claim: closeOnce(claim)}
	copied := handoff

	if err := handoff.Release(); err != nil {
		t.Fatalf("Release() = %v", err)
	}
	if err := copied.Release(); err != nil {
		t.Fatalf("the second Release() = %v, want nil", err)
	}

	if claim.closed != 1 {
		t.Errorf("the claim was given up %d times, want exactly 1", claim.closed)
	}
}

// A caller that checks the second release is told what the first one found,
// rather than being told it worked because it had already happened.
func TestGivingUpAClaimTwiceRepeatsTheFirstAnswer(t *testing.T) {
	t.Parallel()

	failing := &counting{err: errors.New("the handle was already gone")}
	claim := closeOnce(failing)

	first := claim.Close()
	if first == nil {
		t.Fatal("Close() = nil, want the error of the claim underneath")
	}
	if second := claim.Close(); !errors.Is(second, first) {
		t.Errorf("the second Close() = %v, want %v", second, first)
	}
	if failing.closed != 1 {
		t.Errorf("the claim was given up %d times, want exactly 1", failing.closed)
	}
}

// The same promise for the claim the operating system actually hands back, on
// whichever platform this is running: a real handle, closed twice.
func TestTheRealClaimCanBeGivenUpTwice(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "claim-*")
	if err != nil {
		t.Fatalf("CreateTemp(...) = %v", err)
	}
	defer func() { _ = file.Close() }()

	claim, err := hold(file)
	if err != nil {
		t.Fatalf("hold(...) = %v", err)
	}

	if err := claim.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := claim.Close(); err != nil {
		t.Errorf("the second Close() = %v, want nil", err)
	}
}
