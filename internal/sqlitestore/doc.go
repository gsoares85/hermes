// Package sqlitestore keeps what the application remembers between runs.
//
// It is the outside of the local state, and the only package in the repository
// allowed to import database/sql or a driver for it: internal/core/store owns
// the concept of a history and the rules it obeys, and this owns the file, the
// schema and the migrations. The dependency gate holds both halves to that.
// See ADR-0014.
//
// The driver is modernc.org/sqlite, which is SQLite transpiled to Go rather
// than bound to it through cgo. The reason is the other binary: cmd/hermes-cli
// imports no Wails and no keychain, so it compiles today with CGO_ENABLED=0
// and runs in an image with no C library at all — which is where a profile in
// a pipeline runs. A driver needing cgo would take that away, and the price of
// the one that does not is a store slower than the C library at a scale this
// file never reaches: one row per operation, one page per opening of a panel.
package sqlitestore
