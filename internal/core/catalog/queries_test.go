package catalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// The check catches the forms that were confirmed hijackable against a server,
// and says nothing about the forms these queries are written in.
//
// The two tests above cannot show either. They read the constants this package
// holds, and those hold none of the dangerous forms — by construction, since
// the check refuses them — so a check that had quietly stopped looking would
// make both pass. Which is what happened: three designs of this check have
// failed in a row, each on a form it did not look for, and each failure was
// found by a person rather than by the suite.
//
// So the forms live here, as a table. Adding a line to it is what closing the
// next hole looks like.
func TestTheCheckCatchesWhatWasConfirmedHijackable(t *testing.T) {
	t.Parallel()

	for sql, want := range map[string]string{
		"SELECT 1 WHERE a = b":                                "the operator =",
		"SELECT 1 WHERE a!=b":                                 "the operator !=",
		"SELECT 1 WHERE a IN (1, 2)":                          "the comparison IN",
		"SELECT 1 WHERE a NOT IN (1, 2)":                      "the comparison NOT IN",
		"SELECT 1 WHERE a LIKE 'x'":                           "the comparison LIKE",
		"SELECT 1 WHERE a IS DISTINCT FROM b":                 "the comparison IS DISTINCT FROM",
		"SELECT unnest(i.indkey) FROM pg_catalog.pg_index i":  "the function unnest",
		"SELECT CASE d.classid WHEN 'c'::regclass THEN 1 END": "the comparison CASE d.classid WHEN",
	} {
		found := unqualified(sql)
		if !slices.Contains(found, want) {
			t.Errorf("the check reports %v for %q; it has to report %q", found, sql, want)
		}
	}
}

// And the forms the queries are actually written in are quiet, because a check
// that complains about those is a check somebody turns off.
func TestTheCheckIsQuietAboutTheFormsTheQueriesUse(t *testing.T) {
	t.Parallel()

	for _, sql := range []string{
		"SELECT c.relname FROM pg_catalog.pg_class c" +
			" WHERE c.relname OPERATOR(pg_catalog.=) $1",
		"SELECT CASE WHEN a OPERATOR(pg_catalog.=) b THEN 1 ELSE 0 END",
		"SELECT pg_catalog.array_agg(p.relname ORDER BY i.inhseqno) AS inherits",
		"SELECT k.attnum FROM pg_catalog.pg_index i," +
			" LATERAL pg_catalog.unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)",
		"SELECT 1 WHERE c.relkind OPERATOR(pg_catalog.=) ANY (ARRAY['r', 'p'])",
	} {
		if found := unqualified(sql); len(found) != 0 {
			t.Errorf("the check reports %v for %q, which resolves nothing by name", found, sql)
		}
	}
}

// The crossing follows a helper that is handed a query and sends it.
//
// One indirection is allowed, and it is allowed on the grounds that the
// helper's callers are read like anything else. They were not: only Query and
// Exec counted as sending, so a call to the helper was not a send at all, and a
// query built with a + and handed through it was invisible to both checks —
// which is the one shape the crossing exists to refuse.
//
// Written against sources this test supplies rather than the package's own,
// because the package holds no such query by construction: the check refuses
// them, so there is nothing to read.
func TestTheCrossingFollowsAHelperThatSendsWhatItIsGiven(t *testing.T) {
	t.Parallel()

	sent := sends(map[string]*ast.File{"fake.go": parseText(t, `package catalog

const listThings = "SELECT 1"

func (r *Reader) setting(ctx context.Context, sql string, args ...any) error {
	return r.server.Exec(ctx, sql, args...)
}

func (r *Reader) checked(ctx context.Context) error {
	return r.setting(ctx, listThings)
}

func (r *Reader) built(ctx context.Context, name string) error {
	return r.setting(ctx, "SELECT "+name)
}
`)})

	want := []string{
		"the identifier listThings in fake.go",
		"a literal or an expression in fake.go",
	}

	if !slices.Equal(sent, want) {
		t.Errorf("the crossing answered %q, want %q", sent, want)
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

	// CASE with an expression before the first WHEN. It desugars to an = per
	// branch, between that expression and each WHEN, resolved the ordinary way
	// — and there is no operator in the text to qualify, so the form itself is
	// the only thing that can be refused.
	//
	// Confirmed against a server, on the pair this package's own dependency
	// query compares: CASE d.classid WHEN 'pg_catalog.pg_class'::regclass
	// answered nothing under a hijacked path where the searched form answered
	// every row. It is also the rewrite anybody would make to shorten that
	// query, which is how this package regressed into the CVE the last two
	// times — a shorter, more natural form replacing a verbose one.
	simple = regexp.MustCompile(`(?i)\bCASE\b\s+(\S+)`)

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

	for _, branch := range simple.FindAllStringSubmatch(bare, -1) {
		if !strings.EqualFold(branch[1], "WHEN") {
			found = append(found, "the comparison CASE "+branch[1]+" WHEN")
		}
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

	for source, file := range sources(t) {
		for name, value := range constantsIn(t, file) {
			if strings.Contains(value, "SELECT") {
				queries["the identifier "+name+" in "+source] = value
			}
		}
	}

	if len(queries) == 0 {
		t.Fatal("no query constant was found, so every check that reads them is vacuous")
	}

	return queries
}

// queriesSent answers how every call to the server names the SQL it sends.
func queriesSent(t *testing.T) []string {
	t.Helper()

	sent := sends(sources(t))
	if len(sent) == 0 {
		t.Fatal("no query is sent anywhere, so the crossing checks nothing")
	}

	return sent
}

// sends is the crossing over already-parsed sources, so a test can hand it ones
// it wrote rather than only the package's own.
//
// An identifier is answered by name so it can be crossed against the constants;
// anything else is answered as the shape it is, which never matches and so is
// reported.
func sends(files map[string]*ast.File) []string {
	carriers := carriersIn(files)

	var sent []string

	for _, source := range slices.Sorted(maps.Keys(files)) {
		for _, declaration := range files[source].Decls {
			if function, isFunction := declaration.(*ast.FuncDecl); isFunction {
				sent = append(sent, queriesIn(function, source, carriers)...)
			}
		}
	}

	return sent
}

// carriersIn answers the functions of the package that take SQL and send it,
// by the position of the argument they take it in.
//
// It exists because the crossing allows exactly one indirection — a helper that
// is handed a query and hands it to the connection — and the reason that is
// allowed is that the helper's callers are read like anything else. They were
// not. Only Query and Exec counted as sending, so a call to the helper was not
// a send at all, and a query built with a + and passed through it was invisible
// to both checks. Verified by writing one: neither reported a thing.
//
// To a fixed point, because a helper that calls a helper is the same shape and
// one pass finds only the innermost of them.
func carriersIn(files map[string]*ast.File) map[string]int {
	carriers := map[string]int{}

	for grew := true; grew; {
		grew = false

		for _, file := range files {
			for _, declaration := range file.Decls {
				function, isFunction := declaration.(*ast.FuncDecl)
				if !isFunction {
					continue
				}

				carried, carries := carried(function, carriers)
				if carries && carriers[function.Name.Name] != carried {
					carriers[function.Name.Name] = carried
					grew = true
				}
			}
		}
	}

	return carriers
}

// carried answers which of a function's parameters it hands to the connection.
func carried(function *ast.FuncDecl, carriers map[string]int) (int, bool) {
	positions := map[string]int{}

	if function.Type.Params != nil {
		for _, parameter := range function.Type.Params.List {
			for _, name := range parameter.Names {
				positions[name.Name] = len(positions)
			}
		}
	}

	carried, carries := 0, false

	ast.Inspect(function, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}

		holds, sends := sqlArgument(call, carriers)
		if !sends {
			return true
		}

		if name, isName := call.Args[holds].(*ast.Ident); isName {
			if position, isParameter := positions[name.Name]; isParameter {
				carried, carries = position, true
			}
		}

		return true
	})

	return carried, carries
}

// queriesIn answers how one function names the SQL it sends.
//
// The one parameter it forwards is answered as though it were checked, because
// carriersIn has made every call to this function a send in its own right and
// the constants of those callers are read like any other. Any other identifier
// is not — that is where a query built with Sprintf would land, and it is the
// shape this crossing exists to refuse.
func queriesIn(function *ast.FuncDecl, source string, carriers map[string]int) []string {
	forwarded := ""

	if carried, carries := carriers[function.Name.Name]; carries {
		forwarded = parameterAt(function, carried)
	}

	var sent []string

	ast.Inspect(function, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}

		holds, sends := sqlArgument(call, carriers)
		if !sends {
			return true
		}

		name, isName := call.Args[holds].(*ast.Ident)

		switch {
		case isName && name.Name == forwarded:
			// Handed in by a caller, whose own call is a send read elsewhere.
		case isName:
			sent = append(sent, "the identifier "+name.Name+" in "+source)
		default:
			sent = append(sent, "a literal or an expression in "+source)
		}

		return true
	})

	return sent
}

// parameterAt answers the name of a function's nth parameter.
func parameterAt(function *ast.FuncDecl, wanted int) string {
	at := 0

	for _, parameter := range function.Type.Params.List {
		for _, name := range parameter.Names {
			if at == wanted {
				return name.Name
			}

			at++
		}
	}

	return ""
}

// sqlArgument answers where a call carries SQL, and whether it carries any.
//
// The two methods that hand SQL to a connection take it second, after the
// context; a carrier takes it wherever it declared it.
func sqlArgument(call *ast.CallExpr, carriers map[string]int) (int, bool) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if carried, carries := carriers[fun.Sel.Name]; carries && carried < len(call.Args) {
			return carried, true
		}

		switch fun.Sel.Name {
		case "Query", "Exec":
			return 1, len(call.Args) >= 2
		}
	case *ast.Ident:
		if carried, carries := carriers[fun.Name]; carries && carried < len(call.Args) {
			return carried, true
		}
	}

	return 0, false
}

// sources answers the package's own non-test files, parsed, by file name.
func sources(t *testing.T) map[string]*ast.File {
	t.Helper()

	found, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("looking for the sources: %v", err)
	}

	files := map[string]*ast.File{}

	for _, source := range found {
		if !strings.HasSuffix(source, "_test.go") {
			files[source] = parse(t, source)
		}
	}

	return files
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

// parseText parses source a test wrote, so the crossing can be given shapes
// this package does not contain.
func parseText(t *testing.T, text string) *ast.File {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "fake.go", text, 0)
	if err != nil {
		t.Fatalf("parsing the source this test wrote: %v", err)
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
