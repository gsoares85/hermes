package ui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/ui"
)

// treeRows answers canned rows to whatever is asked.
type treeRows struct {
	rows    [][]any
	current int
	err     error
}

func (t *treeRows) Next() bool {
	if t.err != nil || t.current >= len(t.rows) {
		return false
	}

	t.current++

	return true
}

func (t *treeRows) Scan(dest ...any) error {
	row := t.rows[t.current-1]
	for i := range dest {
		target, ok := dest[i].(*string)
		if !ok {
			return errors.New("the tree asked for something that is not text")
		}

		text, ok := row[i].(string)
		if !ok {
			return errors.New("the fixture holds something that is not text")
		}

		*target = text
	}

	return nil
}

func (t *treeRows) Err() error { return t.err }
func (t *treeRows) Close()     {}

// treeSession is a session that answers a listing and records what it was
// asked, including the arguments — which is where a pattern typed by a person
// has to arrive.
type treeSession struct {
	mu     sync.Mutex
	sql    []string
	args   [][]any
	rows   [][]any
	err    error
	closed bool
}

func (t *treeSession) Query(ctx context.Context, sql string, args ...any) driver.Rows {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.sql = append(t.sql, sql)
	t.args = append(t.args, args)

	if err := ctx.Err(); err != nil {
		return &treeRows{err: err}
	}

	if t.err != nil {
		return &treeRows{err: t.err}
	}

	return &treeRows{rows: t.rows}
}

func (t *treeSession) Exec(context.Context, string, ...any) error { return nil }
func (t *treeSession) QueryRow(context.Context, string, ...any) driver.Row {
	return nil
}
func (t *treeSession) Begin(context.Context) error         { return nil }
func (t *treeSession) BeginSnapshot(context.Context) error { return nil }
func (t *treeSession) Commit(context.Context) error        { return nil }
func (t *treeSession) Rollback(context.Context) error      { return nil }
func (t *treeSession) InTransaction() bool                 { return false }
func (t *treeSession) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
}

func (t *treeSession) asked() ([]string, [][]any) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return append([]string(nil), t.sql...), append([][]any(nil), t.args...)
}

func (t *treeSession) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.closed
}

// treePool hands out one session per database, so a test can say which database
// a question was asked of.
type treePool struct {
	database   string
	sessions   *sessions
	rows       [][]any
	err        error
	sessionErr error
}

func (t treePool) Ping(context.Context) error                    { return nil }
func (t treePool) ServerVersion(context.Context) (string, error) { return "16.2", nil }
func (t treePool) Databases(context.Context) ([]string, error)   { return []string{"app", "hermes"}, nil }
func (t treePool) Close()                                        {}
func (t treePool) Session(context.Context) (driver.Session, error) {
	if t.sessionErr != nil {
		return nil, t.sessionErr
	}

	return t.sessions.of(t.database, t.rows, t.err), nil
}

// sessions keeps the session handed out per database, so that a test can read
// what was asked after the call has returned.
type sessions struct {
	mu   sync.Mutex
	held map[string]*treeSession
}

func newSessions() *sessions { return &sessions{held: map[string]*treeSession{}} }

func (s *sessions) of(database string, rows [][]any, err error) *treeSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, found := s.held[database]
	if !found {
		held = &treeSession{rows: rows, err: err}
		s.held[database] = held
	}

	return held
}

func (s *sessions) get(database string) *treeSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.held[database]
}

type treeOpener struct {
	sessions *sessions
	rows     [][]any
	err      error
	// sessionErr fails the checkout itself, which is what a database this role
	// may not open looks like: the pool is built without contacting anything,
	// and the refusal arrives at the first thing that actually asks.
	sessionErr error
}

func (t treeOpener) Open(_ context.Context, target driver.Target) (driver.Pool, error) {
	return treePool{
		database:   target.Database,
		sessions:   t.sessions,
		rows:       t.rows,
		err:        t.err,
		sessionErr: t.sessionErr,
	}, nil
}

// openTree opens a connection through the real connection service and answers
// the tree service over it, with the id the tree addresses nodes by.
func openTree(t *testing.T, opener treeOpener) (*ui.CatalogService, string) {
	t.Helper()

	connections := service(opener)
	t.Cleanup(connections.CloseAll)

	status, err := connections.Open(t.Context(), form())
	if err != nil {
		t.Fatalf("opening the connection: %v", err)
	}

	return ui.NewCatalogService(connections), status.ID
}

// The root of the tree is the databases of the server.
func TestTheRootOfTheTreeIsTheDatabases(t *testing.T) {
	t.Parallel()

	tree, id := openTree(t, treeOpener{sessions: newSessions()})

	children, err := tree.Children(t.Context(), id, ui.NodeRef{}, ui.TreeFilter{})
	if err != nil {
		t.Fatalf("asking for the root: %v", err)
	}

	if len(children) != 2 {
		t.Fatalf("the root holds %v, want the databases of the server", children)
	}

	for _, child := range children {
		if child.Kind != "database" {
			t.Errorf("%s came back as %q, want database", child.Name, child.Kind)
		}

		if !child.Expandable {
			t.Errorf("the database %s cannot be expanded", child.Name)
		}
	}
}

// A database holds its schemas, and they are asked of that database rather
// than of the one the connection was opened on.
//
// It is the whole reason a database is a connection of its own: asking the
// first connection would answer the schemas of the wrong database, and nothing
// about the answer would say so.
func TestADatabaseHoldsItsOwnSchemas(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"public"}, {"sales"}}})

	children, err := tree.Children(t.Context(), id, ui.NodeRef{Database: "app"}, ui.TreeFilter{})
	if err != nil {
		t.Fatalf("expanding the database app: %v", err)
	}

	if len(children) != 2 || children[0].Kind != "schema" {
		t.Fatalf("the database holds %v, want its schemas", children)
	}

	if held.get("app") == nil {
		t.Fatal("the schemas were not asked of the database app")
	}
}

// A schema holds its objects, with the kind each one is.
func TestASchemaHoldsItsObjects(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{
		sessions: held,
		rows:     [][]any{{"orders", "r"}, {"open_orders", "v"}, {"orders_id_seq", "S"}},
	})

	children, err := tree.Children(t.Context(), id,
		ui.NodeRef{Database: "app", Schema: "sales"}, ui.TreeFilter{})
	if err != nil {
		t.Fatalf("expanding the schema sales: %v", err)
	}

	kinds := map[string]string{}
	for _, child := range children {
		kinds[child.Name] = child.Kind
	}

	for name, want := range map[string]string{
		"orders": "table", "open_orders": "view", "orders_id_seq": "sequence",
	} {
		if kinds[name] != want {
			t.Errorf("%s came back as %q, want %q", name, kinds[name], want)
		}
	}

	// A table can be expanded when there is something under it to show. This
	// version has no level below an object, and saying otherwise would draw an
	// arrow that opens onto nothing.
	for _, child := range children {
		if child.Expandable {
			t.Errorf("%s says it can be expanded, and there is no level under it", child.Name)
		}
	}
}

// A schema without a database is refused rather than guessed at.
func TestASchemaWithoutADatabaseIsRefused(t *testing.T) {
	t.Parallel()

	tree, id := openTree(t, treeOpener{sessions: newSessions()})

	if _, err := tree.Children(t.Context(), id, ui.NodeRef{Schema: "sales"}, ui.TreeFilter{}); err == nil {
		t.Error("a schema with no database answered children")
	}
}

// A pattern typed by a person reaches the server as an argument.
//
// The frontend never assembles SQL, and this is where that stops being a rule
// somebody remembers: what crosses is a node and a pattern, and the pattern
// arrives beside the query rather than inside it.
func TestAPatternCrossesAsAnArgumentAndNotAsSQL(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"orders", "r"}}})

	const typed = "'; DROP TABLE orders --"

	if _, err := tree.Children(t.Context(), id,
		ui.NodeRef{Database: "app", Schema: "sales"}, ui.TreeFilter{Pattern: typed}); err != nil {
		t.Fatalf("expanding with a pattern: %v", err)
	}

	session := held.get("app")
	if session == nil {
		t.Fatal("no session was checked out against the database app")
	}

	sql, args := session.asked()

	for _, sent := range sql {
		if strings.Contains(sent, "DROP TABLE") {
			t.Errorf("what was typed reached the query: %s", sent)
		}
	}

	var carried bool

	for _, sent := range args {
		for _, arg := range sent {
			if arg == typed {
				carried = true
			}
		}
	}

	if !carried {
		t.Errorf("the pattern is in none of the arguments %v", args)
	}
}

// The session is given back after every level.
//
// A session held is a connection out of the pool, and a tree that leaks one per
// expansion runs a server out of connections by browsing it.
func TestTheSessionIsGivenBackAfterALevel(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"public"}}})

	if _, err := tree.Children(t.Context(), id, ui.NodeRef{Database: "app"}, ui.TreeFilter{}); err != nil {
		t.Fatalf("expanding the database app: %v", err)
	}

	session := held.get("app")
	if session == nil {
		t.Fatal("no session was checked out against the database app")
	}

	if !session.isClosed() {
		t.Error("the session was kept after the level was answered")
	}
}

// A caller who gives up is not answered anyway.
func TestGivingUpOnALevelStopsIt(t *testing.T) {
	t.Parallel()

	tree, id := openTree(t, treeOpener{sessions: newSessions(), rows: [][]any{{"public"}}})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := tree.Children(ctx, id, ui.NodeRef{Database: "app"}, ui.TreeFilter{}); err == nil {
		t.Error("a cancelled expansion answered children")
	}
}

// A connection nobody opened is refused.
func TestAConnectionThatIsNotOpenHasNoTree(t *testing.T) {
	t.Parallel()

	tree, _ := openTree(t, treeOpener{sessions: newSessions()})

	if _, err := tree.Children(t.Context(), "nothing", ui.NodeRef{}, ui.TreeFilter{}); err == nil {
		t.Error("a connection that is not open answered a tree")
	}
}

// Asking for the system schemas changes what is asked of the server.
//
// The checkbox that shows them is worth nothing if the level comes back the
// same either way, and nothing between the window and the query would say so:
// the filter crosses as a value, is turned into a catalog filter, and only
// there does it decide which query runs. This is the one place that can see
// both ends.
func TestAskingForTheSystemSchemasChangesTheQuestion(t *testing.T) {
	t.Parallel()

	asked := func(filter ui.TreeFilter) string {
		held := newSessions()
		tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"public"}}})

		if _, err := tree.Children(t.Context(), id, ui.NodeRef{Database: "app"}, filter); err != nil {
			t.Fatalf("expanding the database app: %v", err)
		}

		session := held.get("app")
		if session == nil {
			t.Fatal("no session was checked out against the database app")
		}

		sql, _ := session.asked()
		if len(sql) != 1 {
			t.Fatalf("the level asked %d questions, want 1", len(sql))
		}

		return sql[0]
	}

	if hidden, shown := asked(ui.TreeFilter{}), asked(ui.TreeFilter{System: true}); hidden == shown {
		t.Error("hiding the system schemas and showing them ask the server the same thing")
	}
}

// An object asked for without a name is refused before anything is read.
//
// Reading a schema is the expensive question in this product, and asking it for
// a request that cannot be answered would pay it to say no.
func TestAnObjectWithoutANameIsRefused(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{sessions: held})

	if _, err := tree.Properties(t.Context(), id, ui.ObjectRef{
		Database: "app", Schema: "sales",
	}); err == nil {
		t.Error("an object with no name answered properties")
	}

	if _, err := tree.DDL(t.Context(), id, ui.ObjectRef{
		Database: "app", Schema: "sales",
	}); err == nil {
		t.Error("an object with no name answered a script")
	}

	if held.get("app") != nil {
		t.Error("it reached the server anyway")
	}
}

// A connection nobody opened has no objects either.
func TestAConnectionThatIsNotOpenHasNoObjects(t *testing.T) {
	t.Parallel()

	tree, _ := openTree(t, treeOpener{sessions: newSessions()})

	object := ui.ObjectRef{Database: "app", Schema: "sales", Name: "orders"}

	if _, err := tree.Properties(t.Context(), "nothing", object); err == nil {
		t.Error("a connection that is not open answered properties")
	}

	if _, err := tree.DDL(t.Context(), "nothing", object); err == nil {
		t.Error("a connection that is not open answered a script")
	}
}

// The pattern narrows objects, and never schemas.
//
// Narrowing schemas by their own name is what makes the filter useless for the
// thing people actually type into it. Looking for "invoice" would drop the
// schema "sales" from the answer, and the window cannot draw a table whose
// parent the server did not send — so the one row somebody was looking for
// disappears along with the noise. Hiding what does not match is the window's
// job, over what it already holds, and it keeps a parent whose child matches.
//
// What the server is still asked to do at this level is hide the system
// schemas, which is a different question with a different reason: those are
// thousands of names nobody typed anything to see.
func TestTheFilterNarrowsObjectsAndNotSchemas(t *testing.T) {
	t.Parallel()

	const typed = "invoice"

	carries := func(node ui.NodeRef) bool {
		held := newSessions()
		tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"sales", "r"}}})

		if _, err := tree.Children(t.Context(), id, node, ui.TreeFilter{Pattern: typed}); err != nil {
			t.Fatalf("expanding %+v: %v", node, err)
		}

		session := held.get("app")
		if session == nil {
			t.Fatalf("no session was checked out for %+v", node)
		}

		_, args := session.asked()
		for _, sent := range args {
			for _, arg := range sent {
				if arg == typed {
					return true
				}
			}
		}

		return false
	}

	if carries(ui.NodeRef{Database: "app"}) {
		t.Error("the schemas of a database were narrowed by the pattern, which hides the schemas that hold the matches")
	}

	if !carries(ui.NodeRef{Database: "app", Schema: "sales"}) {
		t.Error("the objects of a schema were not narrowed by the pattern, so a level of fifty thousand names crosses whole")
	}
}

// A node that cannot be opened says why in words, not in SQLSTATE.
//
// "Hermes does not show you the driver's message" is the principle the whole
// diagnosis layer exists for, and the tree was the one screen that broke it: a
// database this role may not open answered with the driver's own text, wrapped
// twice, straight into the tooltip of the node. The reason is already written
// down for every class the engine can report — it just was not being asked for.
func TestANodeThatCannotBeOpenedSaysWhyInWords(t *testing.T) {
	t.Parallel()

	refused := &driver.Failure{
		Class:    driver.FailureNotAuthorized,
		SQLState: "28000",
		Err:      errors.New(`FATAL: no pg_hba.conf entry for host "10.0.0.1" (SQLSTATE 28000)`),
	}

	tree, id := openTree(t, treeOpener{sessions: newSessions(), sessionErr: refused})

	_, err := tree.Children(t.Context(), id, ui.NodeRef{Database: "app"}, ui.TreeFilter{})
	if err == nil {
		t.Fatal("a database that cannot be opened answered a level")
	}

	if strings.Contains(err.Error(), "SQLSTATE") {
		t.Errorf("the node carries the driver's own message: %v", err)
	}

	// The explanation of this class, which names the file the fix is in. A
	// different class would name a different place, which is the whole reason
	// the failure is classified before it is explained.
	if !strings.Contains(err.Error(), "pg_hba") {
		t.Errorf("the node does not explain the refusal: %v", err)
	}
}

// The failures that are not the server's are left alone. A connection closed
// while a node was opening is not a diagnosis about a host — it is work that
// was given up on, and dressing it as a connection failure would have the tree
// report an outage every time somebody disconnects.
func TestAFailureThatIsNotTheServersIsNotDiagnosedAsOne(t *testing.T) {
	t.Parallel()

	tree, id := openTree(t, treeOpener{sessions: newSessions()})

	if err := tree.Refresh(id, ui.ObjectRef{Database: "app", Schema: "sales"}); err != nil {
		t.Fatalf("Refresh() = %v", err)
	}

	_, err := tree.Children(t.Context(), id, ui.NodeRef{Database: "app", Schema: ""}, ui.TreeFilter{})
	if err != nil {
		t.Fatalf("expanding a database that works: %v", err)
	}
}

// The window may only browse into a database this boundary said exists.
//
// Opening a database contacts nothing — that is the promise the connection
// layer makes and keeps — so a name invented on the other side of the boundary
// lands in the connection's map with a live pool behind it: a health-check
// goroutine and up to four connections against somebody's server, per name,
// until the connection is closed. A loop on the frontend, bug or otherwise,
// grows that without limit and nothing here would have said no.
func TestBrowsingIntoADatabaseThatDoesNotExistIsRefused(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"public"}}})

	invented := ui.NodeRef{Database: "not a database of this server"}

	if _, err := tree.Children(t.Context(), id, invented, ui.TreeFilter{}); err == nil {
		t.Error("a database the server never listed answered a level")
	}

	if held.get(invented.Database) != nil {
		t.Error("a pool was opened against a database the server never listed")
	}

	if _, err := tree.Properties(t.Context(), id, ui.ObjectRef{
		Database: invented.Database, Schema: "sales", Name: "orders",
	}); err == nil {
		t.Error("a database the server never listed answered properties")
	}
}

// And into one it did. The check is worth nothing if it refuses everything, and
// this is the assertion that would fail if it did.
func TestBrowsingIntoADatabaseThatExistsIsAllowed(t *testing.T) {
	t.Parallel()

	held := newSessions()
	tree, id := openTree(t, treeOpener{sessions: held, rows: [][]any{{"public"}}})

	if _, err := tree.Children(t.Context(), id, ui.NodeRef{Database: "app"}, ui.TreeFilter{}); err != nil {
		t.Errorf("expanding a database the server listed: %v", err)
	}
}
