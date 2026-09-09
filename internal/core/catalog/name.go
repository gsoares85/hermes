package catalog

import "strings"

// Name is the identifier of an object, exactly as pg_catalog holds it.
//
// It is a type rather than a string for two reasons, and neither of them is
// tidiness. The first is that a schema name, a column name and a type name are
// not interchangeable, and a model built out of bare strings lets them be
// swapped silently. The second is comparison: the diff decides that two objects
// are the same object by comparing their names, so what a name compares as is a
// decision, not an implementation detail.
//
// That decision is: byte for byte, unchanged. Nothing here folds case, and that
// is deliberate — Cliente and cliente are two different tables in PostgreSQL,
// and a model that merged them would report a change nobody made. Nothing here
// trims either: a leading space is legitimate inside a quoted identifier, and
// trimming it would rename the object on the way in.
//
// So the normalisation this type performs is not a transformation. It is the
// guarantee about what a name may contain, checked at the one place a name can
// be built, plus the fact that the pipeline never quotes before storing — the
// catalog returns identifiers unquoted and the DDL writer adds quotes when it
// writes. A name that arrived already quoted would compare unequal to the same
// object read the ordinary way, which is a rename reported where nothing was
// renamed, so this refuses to be one.
type Name struct{ raw string }

// NewName builds the name of an object from what the catalog returned.
//
// It cannot fail, and that is the shape the caller needs: the reader turns
// thousands of rows into names, and a constructor returning an error at every
// column would drown the one place errors actually matter. What it can produce
// is a name that is not Valid, which is what the writer and any path taking
// input from outside check.
func NewName(raw string) Name { return Name{raw: raw} }

// Valid reports whether this can address an object.
//
// It is the rule, not a courtesy: an empty name addresses nothing, a NUL is
// truncated by the wire protocol and by every C API between here and the
// server — so two names differing only after one would silently become the
// same object — and a line break cannot survive a round trip through DDL. A
// name that arrived quoted is refused for the reason on the type.
func (n Name) Valid() bool {
	if n.raw == "" {
		return false
	}

	if strings.ContainsAny(n.raw, "\x00\n\r") {
		return false
	}

	return !quoted(n.raw)
}

// String is the identifier itself, so that a name in an error message or a log
// line reads as the object it names rather than as the struct around it.
func (n Name) String() string { return n.raw }

// quoted reports whether the text is already wrapped in the double quotes SQL
// uses for an identifier. Two characters is the shortest that can be: "" is an
// empty quoted identifier, which PostgreSQL itself refuses.
func quoted(raw string) bool {
	return len(raw) >= 2 && strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`)
}
