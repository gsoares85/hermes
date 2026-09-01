//go:build integration

package postgres_test

import (
	"os"
	"testing"

	"github.com/gsoares85/hermes/internal/testsupport"
)

// TestMain owns the servers shared across this package: they outlive any single
// test, so the only place that can release them is here, after every test has
// finished.
func TestMain(m *testing.M) {
	code := m.Run()
	testsupport.StopShared()
	os.Exit(code)
}
