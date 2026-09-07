package catalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every comparison in every catalog query names pg_catalog, checked by reading
// the queries rather than by remembering to.
//
// The read points the session's search path at the schema it is reading, so an
// unqualified name in these queries is resolved against a schema somebody else
// can write to. That is CVE-2018-1058, and this package has been through it
// twice: once for functions, and once for operators after an argument that "an
// exact operator always exists in pg_catalog" turned out to hold only for the
// comparisons that existed when it was written. The first query added after it
// broke it.
//
// The corpus declares shadowing functions and operators, and they are worth
// having — they prove the server behaves as this reasoning claims. But a fixture
// only covers the type pairs somebody thought of, and the pair that went missing
// both times was the one nobody had. This needs no such foresight: it reads the
// SQL and fails on any comparison that is not qualified, whatever the types.
//
// It is deliberately syntactic. A rule that a person has to remember is not a
// rule, and the comment in expr.go says exactly that about the invariant this
// replaces.
func TestEveryComparisonInAQueryNamesPgCatalog(t *testing.T) {
	t.Parallel()

	for name, sql := range catalogQueries(t) {
		for _, comparison := range bareComparisons(sql) {
			t.Errorf("%s compares with a bare %q; a schema on the search path can"+
				" declare an operator that beats it", name, comparison)
		}
	}
}

// bare finds a comparison operator that is not wrapped in OPERATOR(pg_catalog.…).
//
// The negative lookbehind is what does the work: a qualified comparison is
// spelled OPERATOR(pg_catalog.=), so the character is preceded by a dot. Every
// other appearance of one of these characters between spaces is a comparison
// somebody wrote bare.
var bare = regexp.MustCompile(`(?:^|[^.<>=!])\s(<=|>=|<>|=|<|>)\s`)

func bareComparisons(sql string) []string {
	var found []string

	for _, match := range bare.FindAllStringSubmatch(sql, -1) {
		found = append(found, match[1])
	}

	return found
}

// catalogQueries answers every SQL constant of the package, by its name.
//
// Read from the source rather than exported for the test, because exporting
// them would be widening the package's surface for the benefit of one file —
// and because what has to be checked is what is written, not what somebody
// remembered to add to a list.
func catalogQueries(t *testing.T) map[string]string {
	t.Helper()

	queries := map[string]string{}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("looking for the sources: %v", err)
	}

	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}

		for name, value := range constantsIn(t, source) {
			// A query is a constant holding SQL. Nothing else in the package
			// contains a FROM, and a constant that stops containing one has
			// stopped being a query.
			if strings.Contains(value, "SELECT") {
				queries[source+":"+name] = value
			}
		}
	}

	if len(queries) < 7 {
		t.Fatalf("found %d queries, want every one of them: the walk is not finding the sources",
			len(queries))
	}

	return queries
}

func constantsIn(t *testing.T, source string) map[string]string {
	t.Helper()

	text, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("reading %s: %v", source, err)
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), source, text, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", source, err)
	}

	found := map[string]string{}

	for _, declaration := range parsed.Decls {
		general, isGeneral := declaration.(*ast.GenDecl)
		if !isGeneral || general.Tok != token.CONST {
			continue
		}

		for _, spec := range general.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(value.Names) != len(value.Values) {
				continue
			}

			for i, name := range value.Names {
				literal, isLiteral := value.Values[i].(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					continue
				}

				if unquoted, err := strconv.Unquote(literal.Value); err == nil {
					found[name.Name] = unquoted
				}
			}
		}
	}

	return found
}
