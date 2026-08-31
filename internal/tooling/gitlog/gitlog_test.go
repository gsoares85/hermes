package gitlog_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/tooling/gitlog"
)

func TestFormatSeparatesFieldsAndRecords(t *testing.T) {
	t.Parallel()

	got := gitlog.Format("%H", "%an", "%B")
	if want := "--format=%H%x1f%an%x1f%B%x1e"; got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormatOfASingleField(t *testing.T) {
	t.Parallel()

	got := gitlog.Format("%B")
	if want := "--format=%B%x1e"; got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestParseReadsEveryRecord(t *testing.T) {
	t.Parallel()

	output := "abc123\x1fGuilherme\x1ffeat: a\n\x1e\ndef456\x1fGuilherme\x1ffix: b\n\x1e\n"

	got, err := gitlog.Parse(output, 3)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2: %q", len(got), got)
	}
	if got[0][0] != "abc123" || got[0][2] != "feat: a" {
		t.Errorf("first record = %q, want the hash and the subject", got[0])
	}
	if got[1][0] != "def456" || got[1][2] != "fix: b" {
		t.Errorf("second record = %q, want the hash and the subject", got[1])
	}
}

// The separators are control characters precisely so that a commit message can
// carry anything: blank lines, quotes, a diff, a footer. None of it may end a
// record early or shift the fields.
func TestParseKeepsTheBodyIntact(t *testing.T) {
	t.Parallel()

	body := "feat: add thing\n\nA body with \"quotes\", a semicolon; and a\nblank line above.\n\nBREAKING-CHANGE: gone"
	output := "abc123\x1f" + body + "\x1e\n"

	got, err := gitlog.Parse(output, 2)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	if got[0][1] != body {
		t.Errorf("body = %q, want %q", got[0][1], body)
	}
}

func TestParseIgnoresTheEmptyTrailingRecord(t *testing.T) {
	t.Parallel()

	for name, output := range map[string]string{
		"trailing newline":   "abc\x1ffeat: a\x1e\n",
		"no trailing record": "abc\x1ffeat: a\x1e",
		"blank lines":        "\n\nabc\x1ffeat: a\x1e\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := gitlog.Parse(output, 2)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(got) != 1 {
				t.Errorf("got %d records, want 1: %q", len(got), got)
			}
		})
	}
}

func TestParseOfEmptyOutput(t *testing.T) {
	t.Parallel()

	got, err := gitlog.Parse("", 2)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d records, want none", len(got))
	}
}

// A record that does not carry the fields the caller asked for means the format
// and the parser drifted apart. Guessing there would silently check the wrong
// text, so it is an error.
func TestParseRejectsARecordMissingFields(t *testing.T) {
	t.Parallel()

	if _, err := gitlog.Parse("abc\x1ffeat: a\x1e", 3); !errors.Is(err, gitlog.ErrMalformedRecord) {
		t.Errorf("Parse of a short record: error = %v, want ErrMalformedRecord", err)
	}
}

func TestParseRejectsANonPositiveFieldCount(t *testing.T) {
	t.Parallel()

	for _, fields := range []int{0, -1} {
		if _, err := gitlog.Parse("abc\x1e", fields); !errors.Is(err, gitlog.ErrInvalidFieldCount) {
			t.Errorf("Parse(_, %d): error = %v, want ErrInvalidFieldCount", fields, err)
		}
	}
}

// The last field absorbs the rest of the record, so a message that somehow
// carries a separator cannot shift the fields that follow it.
func TestParseGivesTheTrailingSeparatorsToTheLastField(t *testing.T) {
	t.Parallel()

	got, err := gitlog.Parse("abc\x1fbody\x1fwith a separator\x1e", 2)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if want := "body\x1fwith a separator"; got[0][1] != want {
		t.Errorf("last field = %q, want %q", got[0][1], want)
	}
}

func TestMessagesReadsOnlyTheBodies(t *testing.T) {
	t.Parallel()

	got, err := gitlog.Messages("feat: a\x1e\nfix: b\x1e\n")
	if err != nil {
		t.Fatalf("Messages returned error: %v", err)
	}
	if want := []string{"feat: a", "fix: b"}; !equal(got, want) {
		t.Errorf("Messages() = %q, want %q", got, want)
	}
}

func TestMessagesOfEmptyInput(t *testing.T) {
	t.Parallel()

	got, err := gitlog.Messages("\n")
	if err != nil {
		t.Fatalf("Messages returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Messages() = %q, want none", got)
	}
}

func TestIsMergeReadsTheParentList(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"":                    false,
		"abc1234":             false,
		"abc1234 def5678":     true,
		"abc1234 def5678 aaa": true,
	}

	for parents, want := range cases {
		t.Run(strings.ReplaceAll(parents, " ", "_"), func(t *testing.T) {
			t.Parallel()

			if got := gitlog.IsMerge(parents); got != want {
				t.Errorf("IsMerge(%q) = %v, want %v", parents, got, want)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
