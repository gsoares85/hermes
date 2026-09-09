package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/ddl"
	"github.com/gsoares85/hermes/internal/core/secret"
)

// ObjectRef names one object of one schema of one database.
type ObjectRef struct {
	Database string `json:"database"`
	Schema   string `json:"schema"`
	Name     string `json:"name"`
}

// ColumnView is one column as the panel shows it.
type ColumnView struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	NotNull   bool   `json:"notNull"`
	Default   string `json:"default"`
	Identity  string `json:"identity"`
	Generated string `json:"generated"`
	Collation string `json:"collation"`
}

// PropertiesView is what an object is, for the panel beside the tree.
//
// It carries text the window prints rather than the model itself. The model is
// the shape the diff compares, it changes for reasons that have nothing to do
// with what a person reads, and a panel bound to it would drag every one of
// those changes across the boundary.
type PropertiesView struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Columns     []ColumnView `json:"columns"`
	Constraints []string     `json:"constraints"`
	Indexes     []string     `json:"indexes"`

	// Notes are the facts about an object that are not a column: that it is
	// unlogged, that it is a partition, that row security is on. Text, because
	// a person reads them and nothing branches on them.
	Notes []string `json:"notes"`
}

// Properties answers what the selected object is.
func (s *CatalogService) Properties(ctx context.Context, id string, object ObjectRef) (PropertiesView, error) {
	slice, err := s.sliceOf(ctx, id, object)
	if err != nil {
		return PropertiesView{}, err
	}

	return propertiesOf(slice, object.Name), nil
}

// DDL answers the statements that would build the selected object.
//
// Written by the same generator that writes a whole schema, over a model
// holding that one object. A second generator for this panel would be two
// writers of the same statements, and two writers diverge: the day one of them
// learns about a storage parameter, the other is showing a definition that is
// quietly wrong while looking exactly as authoritative.
func (s *CatalogService) DDL(ctx context.Context, id string, object ObjectRef) (string, error) {
	slice, err := s.sliceOf(ctx, id, object)
	if err != nil {
		return "", err
	}

	return ddl.Of(slice).String(), nil
}

// sliceOf reads the schema the object is in and answers a model of that object
// alone.
//
// The read is the expensive one — the whole schema, normalised — and it is the
// right question here rather than in the tree: a panel shows what an object is,
// and what an object is includes everything the model normalises. It is cached
// per connection and database, so the second object somebody clicks in a schema
// costs nothing.
func (s *CatalogService) sliceOf(ctx context.Context, id string, object ObjectRef) (catalog.Schema, error) {
	if object.Name == "" {
		return catalog.Schema{}, errors.New("an object was asked for without a name")
	}

	cache, err := s.cacheOn(ctx, id, object.Database)
	if err != nil {
		return catalog.Schema{}, err
	}

	read, err := cache.Read(ctx, catalog.NewName(object.Schema))
	if err != nil {
		return catalog.Schema{}, secret.Error(err)
	}

	slice, found := read.Only(catalog.NewName(object.Name))
	if !found {
		return catalog.Schema{}, fmt.Errorf("%s.%s is not in the schema any more",
			object.Schema, object.Name)
	}

	return slice, nil
}

// cacheOn answers the metadata cache of one database of one connection.
//
// Per connection and database, which is what ADR-0011 means once a connection
// fans out into one pool per database: what pg_catalog answers depends on the
// role that asked, so two connections under two roles have two legitimately
// different catalogs — and a cache shared between them would show one person
// objects the other cannot see, depending only on who arrived first. A database
// is the same argument one level down: it is a different catalog.
func (s *CatalogService) cacheOn(ctx context.Context, id, database string) (*catalog.Cache, error) {
	connection, err := s.connection(id)
	if err != nil {
		s.forget(id)

		return nil, err
	}

	on, err := connection.Database(ctx, database)
	if err != nil {
		return nil, secret.Error(err)
	}

	key := cacheKey(id, database)

	s.caching.Lock()
	defer s.caching.Unlock()

	if cache, found := s.caches[key]; found {
		return cache, nil
	}

	if s.caches == nil {
		s.caches = map[string]*catalog.Cache{}
	}

	cache := catalog.NewCache(readingThrough{connection: on})
	s.caches[key] = cache

	return cache, nil
}

// forget drops the caches of a connection that is not open any more.
//
// The failed lookup is the moment this can know: nothing tells this service
// that a connection was closed, and a cache of a catalog nobody can reach again
// is memory held for no reason.
func (s *CatalogService) forget(id string) {
	s.caching.Lock()
	defer s.caching.Unlock()

	for key := range s.caches {
		if strings.HasPrefix(key, cacheKey(id, "")) {
			delete(s.caches, key)
		}
	}
}

// cacheKey joins a connection and a database with a NUL, which neither of them
// can contain — an identifier cannot hold one and the identifiers this service
// hands out are hexadecimal.
func cacheKey(id, database string) string { return id + "\x00" + database }

// readingThrough is a cache source that checks out a session per read.
//
// The cache serialises its reads, so one session at a time is all it could use;
// taking one per read rather than holding one is what keeps a browsed database
// from holding a connection out of the pool for as long as its tree is open.
type readingThrough struct{ connection *conn.Connection }

func (r readingThrough) Read(ctx context.Context, schema catalog.Name) (catalog.Schema, error) {
	session, err := r.connection.Session(ctx)
	if err != nil {
		return catalog.Schema{}, err
	}
	defer session.Close()

	return catalog.NewReader(session).Read(ctx, schema)
}

// propertiesOf turns the model of one object into what the panel prints.
func propertiesOf(slice catalog.Schema, name string) PropertiesView {
	for _, table := range slice.Tables {
		if table.Name.String() == name {
			return tableProperties(table)
		}
	}

	for _, view := range slice.Views {
		if view.Name.String() == name {
			return PropertiesView{
				Kind:  string(catalog.ObjectView),
				Name:  name,
				Notes: viewNotes(view),
			}
		}
	}

	for _, sequence := range slice.Sequences {
		if sequence.Name.String() == name {
			return PropertiesView{
				Kind:  string(catalog.ObjectSequence),
				Name:  name,
				Notes: sequenceNotes(sequence),
			}
		}
	}

	return PropertiesView{Name: name}
}

func tableProperties(table catalog.Table) PropertiesView {
	shown := PropertiesView{Kind: string(catalog.ObjectTable), Name: table.Name.String()}

	for _, column := range table.Columns {
		shown.Columns = append(shown.Columns, ColumnView{
			Name:      column.Name.String(),
			Type:      column.Type.String(),
			NotNull:   column.NotNull,
			Default:   column.Default,
			Identity:  column.Identity,
			Generated: column.Generated,
			Collation: column.Collation.String(),
		})
	}

	for _, constraint := range table.Constraints {
		shown.Constraints = append(shown.Constraints, constraint.Name.String()+": "+constraint.Definition)
	}

	for _, index := range table.Indexes {
		shown.Indexes = append(shown.Indexes, index.Definition)
	}

	shown.Notes = tableNotes(table)

	return shown
}

// tableNotes are the facts a copy would behave differently without, which is
// the same reason the model reads them at all.
func tableNotes(table catalog.Table) []string {
	var notes []string

	if table.Unlogged {
		notes = append(notes, "unlogged")
	}

	if table.Partitioned {
		notes = append(notes, "partitioned")
	}

	if table.Partition {
		notes = append(notes, "a partition")
	}

	if table.RowSecurity {
		notes = append(notes, "row security enabled")
	}

	if table.Forced {
		notes = append(notes, "row security forced")
	}

	for _, parent := range table.Inherits {
		notes = append(notes, "inherits "+parent.String())
	}

	return append(notes, table.Options...)
}

func viewNotes(view catalog.View) []string {
	notes := []string{view.Definition}

	if view.CheckOption != "" {
		notes = append(notes, "check option: "+view.CheckOption)
	}

	return append(notes, view.Options...)
}

func sequenceNotes(sequence catalog.Sequence) []string {
	notes := []string{
		fmt.Sprintf("%s starting at %d, by %d", sequence.Type, sequence.Start, sequence.Increment),
		fmt.Sprintf("between %d and %d", sequence.Min, sequence.Max),
	}

	if sequence.Cycle {
		notes = append(notes, "cycles")
	}

	if sequence.OwnedBy.Table.Valid() {
		notes = append(notes,
			"owned by "+sequence.OwnedBy.Table.String()+"."+sequence.OwnedBy.Column.String())
	}

	return notes
}
