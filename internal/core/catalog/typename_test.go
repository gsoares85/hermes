package catalog_test

import (
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// The alias table, which is the reason this type exists. PostgreSQL accepts
// more than one spelling for most built-in types and the catalog can return
// either depending on how the column was declared and on the server version. A
// model that stored the spelling would report int4 against integer as a type
// change, which is the false positive this whole package is arranged to avoid.
func TestATypeNameFoldsTheAliasesOfABuiltInType(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"int4":        "integer",
		"int":         "integer",
		"integer":     "integer",
		"int2":        "smallint",
		"smallint":    "smallint",
		"int8":        "bigint",
		"bigint":      "bigint",
		"bool":        "boolean",
		"boolean":     "boolean",
		"float4":      "real",
		"real":        "real",
		"float8":      "double precision",
		"varchar":     "character varying",
		"char":        "character",
		"decimal":     "numeric",
		"numeric":     "numeric",
		"text":        "text",
		"bytea":       "bytea",
		"uuid":        "uuid",
		"jsonb":       "jsonb",
		"timestamptz": "timestamp with time zone",
		"timetz":      "time with time zone",
	} {
		if got := catalog.NewTypeName(raw).String(); got != want {
			t.Errorf("NewTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The bare spellings mean "without time zone", and the catalog says so out
// loud. Storing the short form for one column and the long form for another
// would make two identical columns compare unequal.
func TestATypeNameSpellsOutWhetherThereIsATimeZone(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"timestamp":                   "timestamp without time zone",
		"timestamp without time zone": "timestamp without time zone",
		"timestamp with time zone":    "timestamp with time zone",
		"time":                        "time without time zone",
		"time without time zone":      "time without time zone",
		"time with time zone":         "time with time zone",
	} {
		if got := catalog.NewTypeName(raw).String(); got != want {
			t.Errorf("NewTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A modifier is part of the type: character varying(50) and character varying
// are different columns. It survives the folding, and it lands where the
// canonical spelling puts it — which for the time zone types is in the middle,
// not at the end, because that is where PostgreSQL itself writes it.
func TestATypeNameKeepsItsModifier(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"varchar(50)":                    "character varying(50)",
		"character varying(50)":          "character varying(50)",
		"numeric(10,2)":                  "numeric(10,2)",
		"decimal(10,2)":                  "numeric(10,2)",
		"numeric(10, 2)":                 "numeric(10,2)",
		"char(3)":                        "character(3)",
		"bpchar(3)":                      "character(3)",
		"timestamptz(3)":                 "timestamp(3) with time zone",
		"timestamp(3) with time zone":    "timestamp(3) with time zone",
		"timestamp(3)":                   "timestamp(3) without time zone",
		"timestamp(3) without time zone": "timestamp(3) without time zone",
		"timetz(6)":                      "time(6) with time zone",
		"time(6)":                        "time(6) without time zone",
	} {
		if got := catalog.NewTypeName(raw).String(); got != want {
			t.Errorf("NewTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// An array is a different type from its element, and the dimensions are part
// of it.
func TestATypeNameKeepsItsArrayDimensions(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"int4[]":        "integer[]",
		"integer[]":     "integer[]",
		"int4[][]":      "integer[][]",
		"varchar(50)[]": "character varying(50)[]",
		"text[]":        "text[]",
	} {
		if got := catalog.NewTypeName(raw).String(); got != want {
			t.Errorf("NewTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A type this table does not know is a type belonging to the person who made
// it: an enum, a domain, a composite. It is left exactly as it came, case
// included, because those names are case sensitive in the same way an object
// name is — and folding one would point a column at a type that does not exist.
func TestATypeNameLeavesATypeOfTheirOwnAlone(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"mood",
		"MyType",
		"public.mood",
		"vendas.estado_pedido",
		"mood[]",
		"hstore",
	} {
		if got := catalog.NewTypeName(raw).String(); got != raw {
			t.Errorf("NewTypeName(%q) = %q, want it unchanged", raw, got)
		}
	}
}

// Whitespace around what the catalog returned is not part of the type.
func TestATypeNameIsTrimmed(t *testing.T) {
	t.Parallel()

	if got := catalog.NewTypeName("  int4  ").String(); got != "integer" {
		t.Errorf(`NewTypeName("  int4  ") = %q, want "integer"`, got)
	}
}

// Case in the alias itself is not meaningful: format_type answers lower case,
// but a type name written by hand in a comparison target may not be.
func TestAnAliasIsFoundWhateverItsCase(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"INT4", "Int4", "VARCHAR(50)", "TIMESTAMPTZ"} {
		if got := catalog.NewTypeName(raw).String(); got == raw {
			t.Errorf("NewTypeName(%q) = %q, want the alias folded", raw, got)
		}
	}
}

// The type is a comparable value, for the same reason a name is: the diff
// compares columns by comparing their types.
func TestATypeNameIsAComparableValue(t *testing.T) {
	t.Parallel()

	if catalog.NewTypeName("int4") != catalog.NewTypeName("integer") {
		t.Error("int4 and integer are not the same type after folding")
	}
	if catalog.NewTypeName("varchar(50)") == catalog.NewTypeName("varchar(51)") {
		t.Error("two different modifiers compare equal")
	}
	if catalog.NewTypeName("integer") == catalog.NewTypeName("integer[]") {
		t.Error("a type and an array of it compare equal")
	}
}

// The zero value is not a type, so a struct field nobody filled in cannot pass
// for one.
func TestTheZeroTypeNameIsNotValid(t *testing.T) {
	t.Parallel()

	var unset catalog.TypeName
	if unset.Valid() {
		t.Error("the zero TypeName is valid")
	}
	if catalog.NewTypeName("   ").Valid() {
		t.Error("a type name of nothing but space is valid")
	}
	if !catalog.NewTypeName("int4").Valid() {
		t.Error("integer is not valid")
	}
}

// bpchar folds when it carries a length and does not when it is bare.
//
// It looks like an inconsistency and is the opposite. bpchar(3) is exactly
// character(3), so folding it is right. A bare bpchar is not a bare character:
// bpchar with no length is the unlimited blank-padded type, while character
// with no length means character(1). Folding it wrote a column of one character
// where the original held any length — data lost by a copy of a structure, and
// invisible until somebody's row came back truncated.
//
// The server draws the same line, which is what makes this a fold of its answer
// rather than a rule invented here: format_type renders character(n) where
// there is a length and bpchar where there is none.
func TestBpcharFoldsOnlyWhenItCarriesALength(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"bpchar(3)": "character(3)",
		"BPCHAR(3)": "character(3)",
		"bpchar":    "bpchar",
		"BPCHAR":    "bpchar",
		"bpchar[]":  "bpchar[]",
	} {
		if got := catalog.NewTypeName(raw).String(); got != want {
			t.Errorf("NewTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A type of somebody's own keeps its name and loses the spacing inside its
// modifier.
//
// The name is untouched because those are case sensitive the way an object name
// is, and folding one would point a column at a type that does not exist. The
// spacing is not part of the type: dom(10, 2) and dom(10,2) are one thing, and a
// model holding both reports a change between a schema and a comparison target
// that says the same as it.
func TestATypeOfTheirOwnKeepsItsNameAndLosesItsSpacing(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"MyType":         "MyType",
		"dom(10, 2)":     "dom(10,2)",
		"dom(10,2)":      "dom(10,2)",
		"MyType(1, 2)[]": "MyType(1,2)[]",
	} {
		if got := catalog.NewTypeName(raw).String(); got != want {
			t.Errorf("NewTypeName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A quoted type name is one piece, parentheses and all.
//
// PostgreSQL allows "my(type)" as an identifier. Reading the bracket inside it
// as a modifier turned the name into "my " with a modifier of (type), and the
// writer would then emit a type that does not exist.
func TestAQuotedTypeNameSurvivesItsPunctuation(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`"my(type)"`, `"weird[]name"`, `"has space"`} {
		if got := catalog.NewTypeName(raw).String(); got != raw {
			t.Errorf("NewTypeName(%q) = %q, want it unchanged", raw, got)
		}
	}
}
