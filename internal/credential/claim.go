package credential

import (
	"io"
	"sync"
)

// closeOnce wraps an operating-system claim so that giving it up twice gives it
// up once.
//
// Handoff is a value and Release has a value receiver, so a copy of a handoff —
// which is what a deferred Release after an explicit one runs against — carries
// a second reference to the same claim and cannot see that the first one has
// already let go. The documented behaviour is that releasing twice is an
// ordinary outcome, so the claim itself has to be what remembers.
//
// It matters most on Windows, where the claim is a raw handle: a number, closed
// unconditionally. The operating system hands the same number to the next
// CreateFile, so the second close lands on somebody else's file — a pgx socket,
// a WebView2 handle — and takes it away with no error anywhere. On Unix the
// second close is merely reported as ErrClosed, which is the same bug surviving
// by luck rather than by design.
//
// The pointer is the point: a copy of the handoff copies the interface value,
// which is this pointer, so every copy shares one sync.Once.
func closeOnce(claim io.Closer) io.Closer {
	return &onceCloser{claim: claim}
}

type onceCloser struct {
	once  sync.Once
	claim io.Closer
	err   error
}

// Close gives the claim up the first time and answers what that first attempt
// answered every time after. Repeating the original error rather than nil is
// what keeps a caller that checks the second call from being told the release
// worked when it did not.
func (c *onceCloser) Close() error {
	c.once.Do(func() { c.err = c.claim.Close() })

	return c.err
}
