package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
)

func target() driver.Target {
	return driver.Target{
		Host: "localhost", Port: 5432,
		Database: "hermes", User: "hermes", Password: "s3cr3t",
		SSLMode: "disable",
	}
}

// Opening must not dial. A saved connection whose host is unreachable has to
// fail when it is used, not when the application starts, and this is the test
// that keeps the cold-start budget honest without needing a database.
func TestOpenDoesNotConnect(t *testing.T) {
	t.Parallel()

	unreachable := target()
	unreachable.Host = "host.invalid"
	unreachable.Port = 1

	pool, err := postgres.New().Open(t.Context(), unreachable)
	if err != nil {
		t.Fatalf("Open reached the network and failed: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err == nil {
		t.Error("Ping to an unreachable host returned no error")
	}
}

// An sslmode the implementation cannot honour yet must be refused. Dropping it
// would connect with less protection than was asked for, and nothing on screen
// would say so.
func TestOpenRefusesAnUnsupportedSSLMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"require", "verify-ca", "verify-full", "allow"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			strict := target()
			strict.SSLMode = mode

			pool, err := postgres.New().Open(t.Context(), strict)
			if !errors.Is(err, postgres.ErrUnsupported) {
				if pool != nil {
					pool.Close()
				}
				t.Fatalf("Open with sslmode=%s error = %v, want ErrUnsupported", mode, err)
			}
			if !strings.Contains(err.Error(), mode) {
				t.Errorf("error %q does not name the mode that was refused", err)
			}
		})
	}
}

func TestOpenAcceptsTheModesThatNeedNoCertificates(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "prefer", "disable"} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Parallel()

			accepted := target()
			accepted.SSLMode = mode

			pool, err := postgres.New().Open(t.Context(), accepted)
			if err != nil {
				t.Fatalf("Open with sslmode=%q returned error: %v", mode, err)
			}
			pool.Close()
		})
	}
}

func TestOpenRejectsAnImpossiblePort(t *testing.T) {
	t.Parallel()

	for _, port := range []int{0, -1, 70000} {
		broken := target()
		broken.Port = port

		if _, err := postgres.New().Open(t.Context(), broken); !errors.Is(err, postgres.ErrUnsupported) {
			t.Errorf("Open with port %d error = %v, want ErrUnsupported", port, err)
		}
	}
}

// Closing twice happens: a job that failed closes the pool, and so does the
// deferred close of whoever opened it.
func TestCloseIsSafeToCallTwice(t *testing.T) {
	t.Parallel()

	pool, err := postgres.New().Open(t.Context(), target())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	pool.Close()
	pool.Close()
}

// Nothing that crosses the seam may be a pgx type, or the core layer would end
// up depending on the driver through a return value.
func TestPoolSatisfiesTheContract(t *testing.T) {
	t.Parallel()

	var opener any = postgres.New()
	if _, ok := opener.(driver.Opener); !ok {
		t.Fatal("New() does not satisfy driver.Opener")
	}

	pool, err := postgres.New().Open(t.Context(), target())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer pool.Close()

	var value any = pool
	if _, ok := value.(driver.Pool); !ok {
		t.Fatal("Open did not return a driver.Pool")
	}
}
