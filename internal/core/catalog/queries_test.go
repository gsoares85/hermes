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

	for _, query := range catalogQueries(t) {
		for _, found := range unqualified(query.sql) {
			t.Errorf("%s resolves %s by name; a schema on the search path can declare"+
				" one that beats the catalog's", query, found)
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
		if _, checked := known[sent.name]; !checked {
			t.Errorf("%s, so nothing checks what it resolves by name", sent)
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

		// A schema that is not pg_catalog is somebody's code, and in
		// PostgreSQL 14 and below public is writable by PUBLIC.
		"SELECT public.thing(1)": "the function public.thing",

		// The same text as an alias list earlier in the query, which used to
		// forgive it.
		"SELECT x FROM t AS k(a), LATERAL k(1)": "the function k",
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

		// The constructs the grammar binds to the catalog, which a check that
		// complained about them would only teach somebody to switch off.
		"SELECT CAST(c.oid AS text), GREATEST(1, 2), NULLIF(1, 2), EXTRACT(YEAR FROM pg_catalog.now())",
		"SELECT pg_catalog.count(*) FILTER (WHERE true) OVER () FROM pg_catalog.pg_class",
		"SELECT 1 WHERE (a, b) OVERLAPS (c, d)",
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

	want := []sending{
		{name: "listThings", source: "fake.go"},
		{source: "fake.go"},
	}

	if !slices.Equal(sent, want) {
		t.Errorf("the crossing answered %v, want %v", sent, want)
	}
}

// A query sent from a function literal held in a var is a send.
//
// The crossing descended into declared functions only, on no stated grounds,
// which left everything else in the file unread. A var holding a func literal
// is the shape that lands there, and the doc on the crossing named "kept in a
// var" as one of the three things it exists to catch.
func TestAQuerySentFromAVarIsFound(t *testing.T) {
	t.Parallel()

	sent := sends(map[string]*ast.File{"fake.go": parseText(t, `package catalog

var read = func(r *Reader, ctx context.Context, name string) error {
	return r.server.Exec(ctx, "SELECT "+name)
}
`)})

	want := []sending{{source: "fake.go"}}
	if !slices.Equal(sent, want) {
		t.Errorf("the crossing answered %v, want %v", sent, want)
	}
}

// Every way a session is handed SQL is a send, QueryRow included.
//
// It is on driver.Session already and the catalog's Querier has not asked for
// it, so this finds nothing in the package today. That is the reason to write
// it: the crossing is what stands between a new query and nobody reading it,
// and a method missing from its list is a query nothing checks on the day
// somebody reaches for the obvious way to read one value.
func TestEveryWayOfHandingSQLToASessionIsASend(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"Query", "QueryRow", "Exec"} {
		sent := sends(map[string]*ast.File{"fake.go": parseText(t, `package catalog

func (r *Reader) one(ctx context.Context, name string) error {
	return r.server.`+method+`(ctx, "SELECT "+name)
}
`)})

		want := []sending{{source: "fake.go"}}
		if !slices.Equal(sent, want) {
			t.Errorf("a query sent through %s is answered %v, want %v", method, sent, want)
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
	//
	// OVERLAPS was here and is not a comparison of that kind. The grammar binds
	// it to the catalog's own functions, and it stays bound with an exact
	// four-argument overlaps() declared first on the path — measured, not
	// assumed. Refusing it only taught somebody to distrust this list.
	worded = regexp.MustCompile(`(?i)\b(NOT\s+IN|IN|BETWEEN|LIKE|ILIKE|SIMILAR\s+TO|` +
		`IS\s+(NOT\s+)?DISTINCT\s+FROM)\b`)

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
// call. Being wrong about the list makes this test noisy, which somebody
// notices; being wrong the other way makes it silent, which is what happened.
//
// Noisy has a cost of its own, though, and it is the one that gets a check
// turned off: the version before this one would have failed the build for
// CAST(, EXTRACT(, FILTER (, OVER ( and GREATEST( — every one of them a
// construct the grammar binds to the catalog, verified against a server with an
// exact shadowing function declared first on the path.
//
// What is deliberately absent is the words that look like the same thing and
// are not. substring, trim, position, overlay, left and right have a grammar of
// their own and are also ordinary functions resolved through the path, so a
// call to one of them has to be qualified like any other.
var keywords = map[string]bool{
	"array": true, "case": true, "coalesce": true, "exists": true,
	"lateral": true, "not": true, "and": true, "or": true, "on": true,
	"values": true, "select": true, "where": true, "from": true, "join": true,
	"union": true, "as": true, "when": true, "then": true, "else": true,
	"end": true, "with": true, "by": true, "in": true, "any": true, "all": true,
	"cast": true, "extract": true, "nullif": true, "greatest": true,
	"least": true, "filter": true, "over": true, "within": true, "group": true,
	"row": true, "using": true, "distinct": true, "having": true,
	"order": true, "partition": true, "returning": true, "overlaps": true,
}

// unqualified answers everything in a query that is resolved by name.
func unqualified(sql string) []string {
	var found []string

	// Literals first — a % or a word inside one is text, not syntax — and then
	// the qualified operators, which are themselves an identifier followed by a
	// parenthesis and would otherwise be reported as calls. The star of an
	// aggregate goes with them: it is the only * in SQL that is not an operator,
	// and count(*) is a thing a query will want.
	//
	// What is left is exactly what the query resolves by name.
	bare := strings.ReplaceAll(
		qualified.ReplaceAllString(literal.ReplaceAllString(sql, "''"), " "), "(*)", "()")

	for _, call := range called.FindAllStringSubmatchIndex(bare, -1) {
		prefix, name := bare[call[2]:call[3]], bare[call[4]:call[5]]

		switch {
		case keywords[strings.ToLower(name)]:
		case strings.HasSuffix(prefix, "pg_catalog."):
		case aliasList(bare, call[0]):
		default:
			// A qualifier that is not pg_catalog is reported rather than
			// forgiven. It used to be forgiven on the grounds that a qualified
			// call is somebody else's business, which is the opposite of true:
			// in PostgreSQL 14 and below public is writable by PUBLIC, so
			// public.anything() is a call into code an attacker can replace.
			found = append(found, "the function "+prefix+name)
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
//
// It takes where the call is rather than what it says. Looking the text up
// answered about the first place it appeared, which is a decision about this
// call made from somewhere else in the query: a bare call was forgiven whenever
// the same text also appeared after an AS earlier on.
func aliasList(sql string, at int) bool {
	before := strings.Fields(sql[:at])

	return len(before) > 0 && strings.EqualFold(before[len(before)-1], "AS")
}

// query is one SQL constant of the package: what it is called, where it is
// written, and what it says.
type query struct{ name, source, sql string }

func (q query) String() string { return "the identifier " + q.name + " in " + q.source }

// catalogQueries answers every SQL constant of the package, by its name.
//
// Read from the source rather than exported for the test: what has to be
// checked is what is written, not what somebody remembered to add to a list.
//
// Keyed by the identifier alone, and not by the identifier and the file it is
// in. Both sides of the crossing were keyed by the pair, so moving a constant
// into a queries.go of its own — a refactor that changes nothing — failed the
// crossing with the words "nothing checks what it resolves by name" about a
// query that is checked. A test that cries wolf at a rename is a test somebody
// stops reading. The file stays in the message, where it helps.
func catalogQueries(t *testing.T) map[string]query {
	t.Helper()

	queries := map[string]query{}

	for source, file := range sources(t) {
		for name, value := range constantsIn(t, file) {
			if strings.Contains(value, "SELECT") {
				queries[name] = query{name: name, source: source, sql: value}
			}
		}
	}

	if len(queries) == 0 {
		t.Fatal("no query constant was found, so every check that reads them is vacuous")
	}

	return queries
}

// sending is one call that hands SQL to a connection: the constant it names, or
// nothing when what it hands over is not a name at all.
type sending struct{ name, source string }

func (s sending) String() string {
	if s.name == "" {
		return "a query is sent from a literal or an expression in " + s.source
	}

	return "a query is sent as " + s.name + " in " + s.source
}

// queriesSent answers how every call to the server names the SQL it sends.
func queriesSent(t *testing.T) []sending {
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
func sends(files map[string]*ast.File) []sending {
	carriers := carriersIn(files)

	var sent []sending

	for _, source := range slices.Sorted(maps.Keys(files)) {
		for _, declaration := range files[source].Decls {
			// Everything that is not a function is walked too, because a
			// function literal held in a var sends queries like any other and
			// descending only into declared functions would not see it. It has
			// no parameter of its own to forgive, which is what the empty name
			// says.
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction {
				sent = append(sent, sendsUnder(declaration, "", source, carriers)...)

				continue
			}

			sent = append(sent, queriesIn(function, source, carriers)...)
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
func queriesIn(function *ast.FuncDecl, source string, carriers map[string]int) []sending {
	forwarded := ""

	if carried, carries := carriers[function.Name.Name]; carries {
		forwarded = parameterAt(function, carried)
	}

	return sendsUnder(function, forwarded, source, carriers)
}

// sendsUnder answers every send under a node, forgiving the one parameter its
// enclosing function forwards.
func sendsUnder(node ast.Node, forwarded, source string, carriers map[string]int) []sending {
	var sent []sending

	ast.Inspect(node, func(node ast.Node) bool {
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
			sent = append(sent, sending{name: name.Name, source: source})
		default:
			sent = append(sent, sending{source: source})
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
// The three methods that hand SQL to a connection take it second, after the
// context; a carrier takes it wherever it declared it.
//
// QueryRow is beside Query and Exec because driver.Session has it and reading a
// single value is the natural thing to want. The catalog's own Querier does not
// expose it yet, so nothing is found by listing it — which is the point: the
// day it is exposed, a query sent through it would otherwise leave the crossing
// without a word.
func sqlArgument(call *ast.CallExpr, carriers map[string]int) (int, bool) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if carried, carries := carriers[fun.Sel.Name]; carries && carried < len(call.Args) {
			return carried, true
		}

		switch fun.Sel.Name {
		case "Query", "QueryRow", "Exec":
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
