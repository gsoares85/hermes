// Package readmecheck finds references in the README to files the repository
// does not publish.
//
// docs/, CLAUDE.md and .claude/ are ignored by Git, so they do not exist for
// anyone reading the project on GitHub. A link to one of them renders as a link
// and leads nowhere, and a mention of one in prose sends the reader looking for
// something they will never find. The README is the only public entry point of
// the project, so it has to be self-contained: if the content matters, it is
// copied in rather than pointed at.
//
// The check lives here, as a package with tests, rather than as a shell
// one-liner in the Makefile — the same reason the commit and coverage gates do.
// A gate written as a recipe runs on whatever shell the platform hands it, and
// the one Windows hands it does not understand the syntax the others do.
package readmecheck

import (
	"regexp"
	"strings"
)

// What is named rather than what a reference to it looks like.
//
// The pattern used to spell out the shapes a reference could take — a Markdown
// link, a backticked path — and so missed docs/README.md, whose second segment
// is uppercase, and any mention in running prose. None of these directories is
// published at all, so naming one is the problem, in any form.
var unpublished = regexp.MustCompile(`docs/|CLAUDE\.md|\.claude/`)

// Reference is one mention of something unpublished, located well enough for
// someone to go and fix it: a gate that only says "no" costs a round trip.
type Reference struct {
	// Line is the 1-indexed line it was found on.
	Line int
	// Match is the text that matched, so the report says which rule fired.
	Match string
	// Text is the whole line, trimmed, for context.
	Text string
}

// Check returns every reference to an unpublished path in the text, in the
// order they appear. A line carrying two of them yields two: reporting the
// first and stopping would send someone round the loop twice.
func Check(text string) []Reference {
	var found []Reference

	for index, line := range strings.Split(text, "\n") {
		for _, match := range unpublished.FindAllString(line, -1) {
			found = append(found, Reference{
				Line:  index + 1,
				Match: match,
				Text:  strings.TrimSpace(line),
			})
		}
	}

	return found
}
