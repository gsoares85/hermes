//go:build integration

package testsupport

import (
	"io"
	"strings"
	"testing"
)

// readAll drains a container stream into a trimmed string.
func readAll(t *testing.T, r io.Reader) string {
	t.Helper()

	content, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading container output: %v", err)
	}

	return strings.TrimSpace(string(content))
}
