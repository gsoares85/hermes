package ddl

import (
	"strings"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// Ident writes an identifier so the server reads back the name that went in.
//
// Always quoted, never only when it has to be. The rule for when quoting can be
// skipped is "not a reserved word and lower case and nothing but letters,
// digits and underscores", and the first clause of it is a list that changes
// between server versions — a name that needed no quotes on 15 and is a keyword
// on 18 produces a statement that parses as something else. Quoting always is
// correct on every version and depends on nothing, and the only thing it costs
// is quotes around names that would have read fine without them.
//
// The doubling is what makes it safe rather than merely tidy. A name is text
// that came from a person by way of the catalog, it may contain a double quote,
// and an identifier written without doubling that quote ends early — after
// which the rest of the name is parsed as SQL. That is the injection this
// function exists to close, and it is closed here rather than at every place
// that writes a name, which is why nothing in this package builds an identifier
// by concatenation.
func Ident(name catalog.Name) string {
	return `"` + strings.ReplaceAll(name.String(), `"`, `""`) + `"`
}

// qualify writes a column of a table, which is the only two-part name the
// model holds.
func qualify(ref catalog.ColumnRef) string {
	return Ident(ref.Table) + "." + Ident(ref.Column)
}
