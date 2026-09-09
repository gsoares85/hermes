package ddl

import "strings"

// The text of a statement is put together here and nowhere else.
//
// Three shapes cover every statement this package writes — parts on one line, a
// head with its options under it, and the parenthesised list a CREATE TABLE
// puts its columns in — and having them in one place is what keeps the output
// the same from one run to the next. A statement assembled inline, with a
// conditional space here and a trailing comma there, is a statement whose text
// depends on which branches ran, and the script would differ between two
// readings of one schema without anything having changed.
//
// Every one of them drops the parts that are not there rather than asking the
// caller to. A column has a collation sometimes and a default sometimes, and a
// caller that had to test each before adding it would grow one branch per
// clause — which is how a stray double space or a dangling keyword gets into
// generated SQL.
const indent = "    "

// clauses joins what belongs on one line.
func clauses(parts ...string) string {
	return strings.Join(present(parts), " ")
}

// options writes a head with the rest under it, one to a line, which is how a
// statement with more parts than fit across a page stays readable.
func options(head string, rest ...string) string {
	return strings.Join(append([]string{head}, present(rest)...), "\n"+indent)
}

// body is the parenthesised list of what a table is made of.
//
// An empty one is written as (), because a table with no columns is legal and
// is exactly the shape that finds a writer assuming there is always something
// between the parentheses.
func body(items []string) string {
	if len(items) == 0 {
		return "()"
	}

	return "(\n" + indent + strings.Join(items, ",\n"+indent) + "\n)"
}

// present drops the parts that are not there.
func present(parts []string) []string {
	written := make([]string, 0, len(parts))

	for _, part := range parts {
		if part != "" {
			written = append(written, part)
		}
	}

	return written
}
