package catalog_test

import (
	"fmt"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// A name is compared by its bytes, and the bytes are the ones the catalog gave.
// Nothing is folded and nothing is trimmed, because both would merge objects
// that are genuinely different — and a diff that merges two tables reports a
// change nobody made.
func TestANameKeepsExactlyWhatTheCatalogGave(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"customers",
		"Customers",
		"CUSTOMERS",
		"orders by região",
		"ação",
		" leading",
		"trailing ",
		`with"quote`,
		"tab\tinside",
	} {
		if got := catalog.NewName(raw).String(); got != raw {
			t.Errorf("NewName(%q).String() = %q, want it unchanged", raw, got)
		}
	}
}

// Two spellings that differ only in case are two different objects in
// PostgreSQL, and the model has to say so. Folding them would be the false
// positive this package exists to avoid, arrived at from the other direction:
// a diff that sees one table where there are two.
func TestNamesThatDifferOnlyInCaseAreDifferentNames(t *testing.T) {
	t.Parallel()

	if catalog.NewName("Cliente") == catalog.NewName("cliente") {
		t.Error(`NewName("Cliente") equals NewName("cliente"), so two tables would compare as one`)
	}
}

// A name is a value: comparable with ==, usable as a map key, and copied by
// assignment. The reader builds maps keyed by name to join one query's rows to
// another's, and that only works if this holds.
func TestANameIsAComparableValue(t *testing.T) {
	t.Parallel()

	built, again := catalog.NewName("orders"), catalog.NewName("orders")
	if built != again {
		t.Error("two names built from the same text are not equal")
	}

	byName := map[catalog.Name]int{built: 1}
	if byName[again] != 1 {
		t.Error("a name does not work as a map key")
	}
}

// Quoting is not stored. The catalog returns identifiers unquoted, the DDL
// writer adds quotes when it writes, and a name that arrived already quoted
// would compare unequal to the same object read the ordinary way — a rename
// reported where nothing was renamed.
func TestANameThatArrivedQuotedIsRefused(t *testing.T) {
	t.Parallel()

	for _, quoted := range []string{`"Cliente"`, `"cliente"`, `""`} {
		if catalog.NewName(quoted).Valid() {
			t.Errorf("NewName(%q) is valid, so a quoted identifier could be stored as a name", quoted)
		}
	}
}

// What cannot be an identifier at all. Empty is nothing to address; a NUL would
// be truncated by the wire protocol and by every C API between here and the
// server, so two names differing after one would silently become the same.
func TestWhatCannotBeAName(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"empty":            "",
		"a NUL":            "a\x00b",
		"a NUL at the end": "ab\x00",
		"a line break":     "a\nb",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if catalog.NewName(raw).Valid() {
				t.Errorf("NewName(%q) is valid", raw)
			}
		})
	}
}

// The zero value is not a name, so a struct field nobody filled in cannot pass
// for one.
func TestTheZeroNameIsNotValid(t *testing.T) {
	t.Parallel()

	var unset catalog.Name
	if unset.Valid() {
		t.Error("the zero Name is valid")
	}
	if unset.String() != "" {
		t.Errorf("the zero Name renders as %q", unset.String())
	}
}

// A name reaches an error message and a log line, so it has to render as
// itself rather than as the struct around it.
func TestANameRendersAsItself(t *testing.T) {
	t.Parallel()

	// Both verbs in one call: a name reaches a message through whichever the
	// caller happened to write, and the struct around it must not show through
	// either.
	if got := fmt.Sprintf("%v|%s", catalog.NewName("Ação"), catalog.NewName("Ação")); got != "Ação|Ação" {
		t.Errorf("a name rendered as %q, want it to render as itself", got)
	}
}
