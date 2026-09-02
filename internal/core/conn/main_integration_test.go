//go:build integration

package conn_test

import (
	"os"
	"testing"

	"github.com/gsoares85/hermes/internal/testsupport"
)

// TestMain releases the servers shared across this package once every test has
// finished, which is the only moment they are certainly no longer needed.
func TestMain(m *testing.M) {
	code := m.Run()
	testsupport.StopShared()
	os.Exit(code)
}
