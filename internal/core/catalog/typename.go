package catalog

import (
	"strings"
)

// TypeName is the type of a column, in the one spelling this model uses.
//
// PostgreSQL accepts several spellings for most built-in types — int4 and
// integer, varchar and character varying, timestamptz and timestamp with time
// zone — and which one comes back depends on how the column was declared and on
// the server version. Storing the spelling would make a diff report a type
// change between two columns that are the same column, which is precisely the
// false positive that costs the product its credibility.
//
// So the folding happens on the way in, at the only place a type can be built.
// The primary source is format_type(atttypid, atttypmod), which already answers
// the canonical form; this table is the net under it, for the spellings that
// reach the model any other way — a comparison target written by hand, an older
// server, a test.
//
// What it does not touch is a type belonging to the person who made it: an
// enum, a domain, a composite. Those names are case sensitive exactly as an
// object name is, and folding one would point a column at a type that does not
// exist.
type TypeName struct{ canonical string }

// canonicalType is the alias table, keyed by the folded spelling.
//
// The value is split around where a modifier goes, because for the time zone
// types that is the middle rather than the end: PostgreSQL writes
// timestamp(3) with time zone, not timestamp with time zone(3). A type whose
// modifier goes at the end simply has an empty tail.
type canonicalType struct{ head, tail string }

var typeAliases = map[string]canonicalType{
	"int":         {head: "integer"},
	"int4":        {head: "integer"},
	"integer":     {head: "integer"},
	"int2":        {head: "smallint"},
	"smallint":    {head: "smallint"},
	"int8":        {head: "bigint"},
	"bigint":      {head: "bigint"},
	"serial":      {head: "integer"},
	"serial4":     {head: "integer"},
	"serial2":     {head: "smallint"},
	"smallserial": {head: "smallint"},
	"serial8":     {head: "bigint"},
	"bigserial":   {head: "bigint"},

	"bool":    {head: "boolean"},
	"boolean": {head: "boolean"},

	"float4":           {head: "real"},
	"real":             {head: "real"},
	"float8":           {head: "double precision"},
	"double precision": {head: "double precision"},

	"decimal": {head: "numeric"},
	"numeric": {head: "numeric"},

	"varchar":           {head: "character varying"},
	"character varying": {head: "character varying"},
	"char":              {head: "character"},
	"bpchar":            {head: "character"},
	"character":         {head: "character"},

	// The bare spellings mean "without time zone", and the catalog says so out
	// loud. Keeping the short form for one column and the long form for another
	// would make two identical columns compare unequal.
	"timestamp":                   {head: "timestamp", tail: "without time zone"},
	"timestamp without time zone": {head: "timestamp", tail: "without time zone"},
	"timestamptz":                 {head: "timestamp", tail: "with time zone"},
	"timestamp with time zone":    {head: "timestamp", tail: "with time zone"},
	"time":                        {head: "time", tail: "without time zone"},
	"time without time zone":      {head: "time", tail: "without time zone"},
	"timetz":                      {head: "time", tail: "with time zone"},
	"time with time zone":         {head: "time", tail: "with time zone"},

	"varbit":      {head: "bit varying"},
	"bit varying": {head: "bit varying"},
}

// NewTypeName folds a type into the spelling this model stores.
//
// It cannot fail, for the same reason NewName cannot: the reader turns a column
// at a time into a type, and a constructor returning an error there would bury
// the failures that matter. A type it cannot make sense of comes back as it
// arrived, which is the right answer for one that belongs to the person who
// made it.
func NewTypeName(raw string) TypeName {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return TypeName{}
	}

	base, modifier, arrays := split(trimmed)

	if isUnlimitedBlankPadded(base, modifier) {
		return TypeName{canonical: unlimitedBlankPadded + arrays}
	}

	canonical, known := typeAliases[strings.ToLower(base)]
	if !known {
		return TypeName{canonical: trimmed}
	}

	return TypeName{canonical: canonical.head + modifier + spaced(canonical.tail) + arrays}
}

// unlimitedBlankPadded is bpchar with no length, which the alias table cannot
// hold because the same word folds one way with a modifier and another without.
const unlimitedBlankPadded = "bpchar"

// isUnlimitedBlankPadded reports whether this is that one spelling.
//
// bpchar with a length is exactly character(n), and folding it is right.
// bpchar without one is not character without one: the bare bpchar is the
// unlimited blank-padded type, while a bare character means character(1). So
// folding the modifier-less spelling wrote a column of a single character where
// the original held any length — data lost by a copy of a structure, and
// invisible until somebody's row was truncated.
//
// format_type draws the same line, which is what makes this a fold of the
// server's own answer rather than a rule invented here: it renders character(n)
// where there is a length and bpchar where there is none.
//
// It is a case here rather than a column in the table because it is the only
// type PostgreSQL has that behaves this way. A field for it would be a field
// that is false everywhere else.
func isUnlimitedBlankPadded(base, modifier string) bool {
	return modifier == "" && strings.EqualFold(base, unlimitedBlankPadded)
}

// String is the canonical spelling, which is what the DDL writes and what the
// diff compares.
func (t TypeName) String() string { return t.canonical }

// Valid reports whether this names a type at all. The zero value does not, so a
// column nobody filled in cannot pass for one that has a type.
func (t TypeName) Valid() bool { return t.canonical != "" }

// split takes a type apart into the three pieces the folding needs: the name,
// the modifier with its parentheses, and the array brackets.
//
// The array brackets come off the end first, then the modifier is found by its
// parentheses. What is left on both sides of the modifier is the name, because
// of the time zone types: in "timestamp(3) with time zone" the name is split in
// two by the modifier, and reading only up to the parenthesis would leave the
// table looking up "timestamp" and losing the time zone.
func split(raw string) (base, modifier, arrays string) {
	body := raw
	for strings.HasSuffix(body, "[]") {
		body = strings.TrimSpace(strings.TrimSuffix(body, "[]"))
		arrays = "[]" + arrays
	}

	open := strings.Index(body, "(")
	closing := strings.LastIndex(body, ")")
	if open < 0 || closing < open {
		return body, "", arrays
	}

	modifier = "(" + compact(body[open+1:closing]) + ")"
	base = strings.TrimSpace(body[:open] + " " + body[closing+1:])

	return strings.Join(strings.Fields(base), " "), modifier, arrays
}

// compact removes the spacing people put inside a modifier, so that
// numeric(10, 2) and numeric(10,2) are one type rather than two.
func compact(modifier string) string {
	parts := strings.Split(modifier, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}

	return strings.Join(parts, ",")
}

func spaced(tail string) string {
	if tail == "" {
		return ""
	}

	return " " + tail
}
