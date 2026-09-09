package ui

import (
	"context"
	"fmt"
	"sync"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/secret"
)

// NodeRef says which node of the tree is being opened.
//
// It is a place rather than a query: the frontend names where it is and gets
// back what is there. Empty is the server, a database alone is that database,
// and a database with a schema is that schema. There is no fourth level in this
// version, which is what Expandable says on the way out.
type NodeRef struct {
	Database string `json:"database"`
	Schema   string `json:"schema"`
}

// TreeFilter narrows a level before it crosses.
//
// It is applied by the server, not here. A level of fifty thousand names
// carried across so that most of them could be dropped is the waste the whole
// listing exists to avoid — and the pattern travels as an argument beside the
// query, never inside it.
type TreeFilter struct {
	// Pattern matches a name as a case-insensitive substring.
	Pattern string `json:"pattern"`

	// System includes the schemas PostgreSQL keeps for itself. It means
	// nothing at the other levels.
	System bool `json:"system"`
}

// NodeView is one row of the object tree.
type NodeView struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Expandable bool   `json:"expandable"`
}

// The kinds a node can be, as the frontend reads them.
const (
	nodeDatabase = "database"
	nodeSchema   = "schema"
)

// CatalogService is the boundary the object tree is drawn from.
//
// One level per call, because that is what lazy means here: expanding a node
// asks what that node holds and nothing more. Reading a schema whole is a
// different question with a different budget, and it belongs to the panel that
// shows an object rather than to the tree that lists them.
//
// No SQL crosses in either direction. What arrives is a place and a filter;
// what goes back is a name, a kind and whether there is anything under it.
type CatalogService struct {
	// connection is how a node finds the connection it belongs to. It is a
	// function rather than the service itself so that nothing here can reach
	// the rest of the connection surface, and so that no method of this type
	// returns something from the core layer — a binding is generated from every
	// exported method, and a pool has no business crossing to the frontend.
	connection func(id string) (*conn.Connection, error)

	// caches are the metadata caches the panel reads through, one per
	// connection and database. See cacheOn for why the key is both.
	caching sync.Mutex
	caches  map[string]*catalog.Cache
}

// NewCatalogService creates the service bound to the frontend.
func NewCatalogService(connections *ConnectionService) *CatalogService {
	return &CatalogService{connection: connections.lookup}
}

// Children answers what a node holds.
//
// The context is the caller's: somebody who expands a node of a large schema
// and gives up before it comes back stops the query rather than waiting for an
// answer nobody wants any more.
func (s *CatalogService) Children(ctx context.Context, id string, node NodeRef,
	filter TreeFilter,
) ([]NodeView, error) {
	connection, err := s.connection(id)
	if err != nil {
		return nil, err
	}

	switch {
	case node.Database == "" && node.Schema != "":
		return nil, fmt.Errorf("the schema %s was asked for without a database", node.Schema)
	case node.Database == "":
		return s.databases(ctx, connection)
	case node.Schema == "":
		return s.schemas(ctx, connection, node.Database, filter)
	default:
		return s.objects(ctx, connection, node, filter)
	}
}

// databases answers the databases of the server, which is the root of the tree.
func (s *CatalogService) databases(ctx context.Context, connection *conn.Connection) ([]NodeView, error) {
	found, err := connection.Databases(ctx)
	if err != nil {
		return nil, secret.Error(err)
	}

	children := make([]NodeView, 0, len(found))
	for _, database := range found {
		children = append(children, NodeView{Kind: nodeDatabase, Name: database, Expandable: true})
	}

	return children, nil
}

// schemas answers the schemas of one database.
//
// Of that database and not of the one the connection was opened on: PostgreSQL
// does not reach across databases, so this goes through the connection the
// database has of its own. Asking the first one would answer the schemas of the
// wrong database, and nothing about the answer would say so.
func (s *CatalogService) schemas(ctx context.Context, connection *conn.Connection,
	database string, filter TreeFilter,
) ([]NodeView, error) {
	lister, done, err := s.listerOn(ctx, connection, database)
	if err != nil {
		return nil, err
	}
	defer done()

	found, err := lister.Schemas(ctx, catalog.Filter{Pattern: filter.Pattern, System: filter.System})
	if err != nil {
		return nil, secret.Error(err)
	}

	children := make([]NodeView, 0, len(found))
	for _, schema := range found {
		children = append(children, NodeView{Kind: nodeSchema, Name: schema.String(), Expandable: true})
	}

	return children, nil
}

// objects answers what a schema holds.
//
// Nothing is expandable: this version has no level below an object, and an
// arrow that opens onto nothing is worse than no arrow.
func (s *CatalogService) objects(ctx context.Context, connection *conn.Connection,
	node NodeRef, filter TreeFilter,
) ([]NodeView, error) {
	lister, done, err := s.listerOn(ctx, connection, node.Database)
	if err != nil {
		return nil, err
	}
	defer done()

	found, err := lister.Objects(ctx, catalog.NewName(node.Schema), filter.Pattern)
	if err != nil {
		return nil, secret.Error(err)
	}

	children := make([]NodeView, 0, len(found))
	for _, object := range found {
		children = append(children, NodeView{Kind: string(object.Kind), Name: object.Name.String()})
	}

	return children, nil
}

// listerOn checks out a session against one database and answers a lister over
// it, together with the call that gives the session back.
//
// The session is held for one level and no longer. A session kept is a
// connection out of the pool, and a tree that leaks one per expansion runs a
// server out of connections by being browsed.
func (s *CatalogService) listerOn(ctx context.Context, connection *conn.Connection,
	database string,
) (*catalog.Lister, func(), error) {
	on, err := connection.Database(ctx, database)
	if err != nil {
		return nil, nil, secret.Error(err)
	}

	session, err := on.Session(ctx)
	if err != nil {
		return nil, nil, secret.Error(err)
	}

	return catalog.NewLister(session), session.Close, nil
}
