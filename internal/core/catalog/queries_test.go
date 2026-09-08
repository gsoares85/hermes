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

// Everything a catalog query resolves by name is written with pg_catalog in
// front of it, checked by reading the queries rather than by remembering to.
//
// The read points the session's search path at the schema it is reading, so any
// name these queries leave unqualified is resolved against a schema somebody
// else can write to. That is CVE-2018-1058, and this package has regressed into
// it twice — once for functions, once for operators.
//
// The first version of this test listed the forms it considered dangerous and
// failed on those. That is a permit list, and a permit list needs its author to
// know every way PostgreSQL can be made to resolve a name. Mine did not: it saw
// `a = b` and missed `a != b`, `a=b`, `a IN (…)`, `a LIKE b` and every function
// call, all of which were then confirmed hijackable against a real server. IN is
// the sharpest of them, because it is the form this code moved away from and the
// form anybody would write by reflex.
//
// So it is a refusal list now. Anything that resolves by name is a failure
// unless it is spelled with pg_catalog, and what is allowed is a short, explicit
// list of the SQL keywords that look like calls. Being wrong about that list
// makes the test noisy, which somebody fixes; being wrong the other way makes it
// silent, which is what happened.
func TestNothingInAQueryResolvesByName(t *testing.T) {
	t.Parallel()

	for name, sql := range catalogQueries(t) {
		for _, found := range unqualified(sql) {
			t.Errorf("%s resolves %s by name; a schema on the search path can declare"+
				" one that beats the catalog's", name, found)
		}
	}
}

// The queries this package sends are the constants it holds, and the test says
// so by crossing the two.
//
// Reading the constants alone leaves a hole the size of the next query somebody
// writes inline, or builds with a +, or keeps in a var — none of which the walk
// would find, and all of which it would pass in silence. So every SQL string
// handed to the server has to be an identifier this file recognises.
func TestEveryQuerySentIsOneThisTestChecked(t *testing.T) {
	t.Parallel()

	known := catalogQueries(t)

	for _, sent := range queriesSent(t) {
		if _, checked := known[sent]; !checked {
			t.Errorf("a query is sent as %s, so nothing checks what it resolves by name", sent)
		}
	}
}

// The three shapes a name can be resolved by, and none of them is allowed bare.
var (
	// A run of the characters PostgreSQL builds operators out of. Anything left
	// after the qualified ones are removed is an operator written bare.
	symbolic = regexp.MustCompile(`[-+*/<>=~!@#%^&|?]+`)

	// The comparisons spelled as words. Each desugars to an operator resolved
	// the ordinary way, so each is as hijackable as the symbol it stands for.
	// IS DISTINCT FROM is matched as the phrase, because DISTINCT on its own is
	// what SELECT DISTINCT is made of.
	worded = regexp.MustCompile(`(?i)\b(NOT\s+IN|IN|BETWEEN|LIKE|ILIKE|SIMILAR\s+TO|` +
		`IS\s+(NOT\s+)?DISTINCT\s+FROM|OVERLAPS)\b`)

	// An identifier followed by an opening parenthesis. A call, unless it is one
	// of the keywords below or an alias list.
	called = regexp.MustCompile(`(?i)([.\w]*?)([a-z_][a-z0-9_]*)\s*\(`)

	// A qualified operator, which is what the check removes before looking.
	qualified = regexp.MustCompile(`OPERATOR\(pg_catalog\.[^)]+\)`)

	// A string literal, removed first so that a % inside one is not read as an
	// operator and a word inside one is not read as SQL.
	literal = regexp.MustCompile(`'[^']*'`)
)

// keywords are the words that are followed by a parenthesis without being a
// call. It is deliberately short and explicit: a keyword missing from it makes
// this test complain about something harmless, which somebody notices and fixes.
// The failure of the version this replaced was in the other direction.
var keywords = map[string]bool{
	"array": true, "case": true, "coalesce": true, "exists": true,
	"lateral": true, "not": true, "and": true, "or": true, "on": true,
	"values": true, "select": true, "where": true, "from": true, "join": true,
	"union": true, "as": true, "when": true, "then": true, "else": true,
	"end": true, "with": true, "by": true, "in": true, "any": true, "all": true,
}

// unqualified answers everything in a query that is resolved by name.
func unqualified(sql string) []string {
	var found []string

	// Literals first — a % or a word inside one is text, not syntax — and then
	// the qualified operators, which are themselves an identifier followed by a
	// parenthesis and would otherwise be reported as calls. What is left is
	// exactly what the query resolves by name.
	bare := qualified.ReplaceAllString(literal.ReplaceAllString(sql, "''"), " ")

	for _, call := range called.FindAllStringSubmatch(bare, -1) {
		prefix, name := call[1], call[2]

		switch {
		case keywords[strings.ToLower(name)]:
		case strings.HasSuffix(prefix, "pg_catalog."):
		case strings.HasSuffix(prefix, "."):
			// A qualified call to something else, or a column of a row type.
			// Neither is this test's business, and neither resolves against the
			// search path the way a bare name does.
		case aliasList(bare, call[0]):
		default:
			found = append(found, "the function "+name)
		}
	}

	for _, word := range worded.FindAllString(bare, -1) {
		found = append(found, "the comparison "+strings.Join(strings.Fields(word), " "))
	}

	// Operators last, with the qualified ones taken out first so that what is
	// left is exactly what was written bare.
	for _, operator := range symbolic.FindAllString(bare, -1) {
		found = append(found, "the operator "+operator)
	}

	return found
}

// aliasList reports whether an identifier followed by a parenthesis is naming
// the columns of a result rather than calling anything — WITH ORDINALITY AS
// k(attnum, ord) is the shape, and AS in front of it is what says so.
func aliasList(sql, call string) bool {
	at := strings.Index(sql, call)
	if at < 0 {
		return false
	}

	before := strings.Fields(sql[:at])

	return len(before) > 0 && strings.EqualFold(before[len(before)-1], "AS")
}

// catalogQueries answers every SQL constant of the package, by its name.
//
// Read from the source rather than exported for the test: what has to be
// checked is what is written, not what somebody remembered to add to a list.
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

		for name, value := range constantsIn(t, parse(t, source)) {
			if strings.Contains(value, "SELECT") {
				queries["the identifier "+name+" in "+source] = value
			}
		}
	}

	return queries
}

// queriesSent answers how every call to the server names the SQL it sends.
//
// An identifier is answered by name so it can be crossed against the constants;
// anything else is answered as the shape it is, which never matches and so is
// reported.
func queriesSent(t *testing.T) []string {
	t.Helper()

	var sent []string

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("looking for the sources: %v", err)
	}

	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}

		for _, declaration := range parse(t, source).Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction {
				continue
			}

			sent = append(sent, queriesIn(function, source)...)
		}
	}

	if len(sent) == 0 {
		t.Fatal("no query is sent anywhere, so the crossing checks nothing")
	}

	return sent
}

// queriesIn answers how one function names the SQL it sends.
//
// A parameter is answered as though it were checked, and that is the one
// indirection allowed: a helper that takes a query and sends it has moved the
// question to its callers, and their constants are read like any other. A local
// variable is not — that is where a query built with Sprintf would land, and it
// is the shape this crossing exists to refuse.
func queriesIn(function *ast.FuncDecl, source string) []string {
	handed := map[string]bool{}

	if function.Type.Params != nil {
		for _, parameter := range function.Type.Params.List {
			for _, name := range parameter.Names {
				handed[name.Name] = true
			}
		}
	}

	var sent []string

	ast.Inspect(function, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall || len(call.Args) < 2 || !sendsToServer(call.Fun) {
			return true
		}

		name, isName := call.Args[1].(*ast.Ident)

		switch {
		case isName && handed[name.Name]:
			// Handed in by a caller, whose own constant is read elsewhere.
		case isName:
			sent = append(sent, "the identifier "+name.Name+" in "+source)
		default:
			sent = append(sent, "a literal or an expression in "+source)
		}

		return true
	})

	return sent
}

// sendsToServer reports whether a call is one of the two that hand SQL to the
// connection.
func sendsToServer(fun ast.Expr) bool {
	selected, isSelected := fun.(*ast.SelectorExpr)

	return isSelected && (selected.Sel.Name == "Query" || selected.Sel.Name == "Exec")
}

func parse(t *testing.T, source string) *ast.File {
	t.Helper()

	text, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("reading %s: %v", source, err)
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), source, text, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", source, err)
	}

	return parsed
}

// constantsIn answers the string constants of a file, whether they are written
// as one literal or built from several.
//
// The concatenated form is here because a long query is natural to write that
// way, and the version of this test that only understood a single literal would
// have passed such a query without reading a character of it.
func constantsIn(t *testing.T, parsed *ast.File) map[string]string {
	t.Helper()

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
				if text, isText := stringOf(value.Values[i]); isText {
					found[name.Name] = text
				}
			}
		}
	}

	return found
}

// stringOf answers the text of a string expression, following the additions of
// a concatenation.
func stringOf(expression ast.Expr) (string, bool) {
	switch node := expression.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}

		text, err := strconv.Unquote(node.Value)

		return text, err == nil
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}

		left, leftIsText := stringOf(node.X)
		right, rightIsText := stringOf(node.Y)

		return left + right, leftIsText && rightIsText
	default:
		return "", false
	}
}
