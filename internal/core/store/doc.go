// Package store defines what this application remembers between runs, and
// holds the double the rest of the core is tested against.
//
// It belongs to the core layer: it must never import UI or framework packages
// and — the reason it is only half of the story — it must never import a
// database. The contract lives here, where it is consumed; the single
// implementation that knows the state is kept in SQLite lives in
// internal/sqlitestore, which is the only package in the repository allowed to
// import database/sql. See ADR-0014 and the docs of the architecture for the
// dependency rule.
//
// The job history is the first thing kept here; the backup catalogue and the
// preferences follow it, into the same file and behind contracts of their own.
package store
