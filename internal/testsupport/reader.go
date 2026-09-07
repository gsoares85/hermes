//go:build integration

package testsupport

import (
	"io"
	"strings"
	"testing"
)

// readAll drains a container stream into a trimmed string.
func readAll(tb testing.TB, r io.Reader) string {
	tb.Helper()

	content, err := io.ReadAll(r)
	if err != nil {
		tb.Fatalf("reading container output: %v", err)
	}

	return strings.TrimSpace(string(content))
}
