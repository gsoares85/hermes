// Package gitlog owns the record format this repository asks `git log` for.
//
// A commit message can contain anything — blank lines, quotes, a whole diff —
// so the fields are separated by the two ASCII control characters that cannot
// appear in one. Both the authorship checker and the release version resolver
// read history through this package, so the format is written and tested once
// instead of being re-derived, slightly differently, in each command.
package gitlog

import (
	"errors"
	"fmt"
	"strings"
)

// Errors returned by this package.
var (
	ErrInvalidFieldCount = errors.New("invalid field count")
	ErrMalformedRecord   = errors.New("malformed git log record")
)

// The bytes that separate fields and records in the output.
const (
	FieldSeparator  = "\x1f"
	RecordSeparator = "\x1e"
)

// The same two bytes as git format placeholders, which is how they are written
// into the --format argument.
const (
	fieldPlaceholder  = "%x1f"
	recordPlaceholder = "%x1e"
)

// Format builds the --format argument that emits one record per commit, with
// the given placeholders as its fields.
func Format(placeholders ...string) string {
	return "--format=" + strings.Join(placeholders, fieldPlaceholder) + recordPlaceholder
}

// Parse splits output produced with Format into one slice of fields per commit.
// The last field absorbs whatever follows it, so a body carrying a separator
// cannot shift the fields around it.
func Parse(output string, fields int) ([][]string, error) {
	if fields < 1 {
		return nil, fmt.Errorf("%w: %d fields requested, want at least one", ErrInvalidFieldCount, fields)
	}

	var records [][]string
	for _, record := range strings.Split(output, RecordSeparator) {
		trimmed := strings.TrimSpace(record)
		if trimmed == "" {
			continue
		}

		parts := strings.SplitN(trimmed, FieldSeparator, fields)
		if len(parts) != fields {
			return nil, fmt.Errorf("%w: %q has %d fields, want %d",
				ErrMalformedRecord, trimmed, len(parts), fields)
		}

		records = append(records, parts)
	}

	return records, nil
}

// Messages reads output produced with Format("%B"): one commit message per
// record and nothing else.
func Messages(output string) ([]string, error) {
	records, err := Parse(output, 1)
	if err != nil {
		return nil, err
	}

	messages := make([]string, 0, len(records))
	for _, record := range records {
		messages = append(messages, record[0])
	}

	return messages, nil
}

// IsMerge reports whether the %P field of a commit lists more than one parent.
func IsMerge(parents string) bool {
	return len(strings.Fields(parents)) > 1
}
