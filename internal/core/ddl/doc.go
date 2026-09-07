// Package ddl writes the DDL of a normalised schema.
//
// It is the other half of the catalog: that package reads a schema into a
// model, this one writes a model back into statements, and the property that
// binds them is the round trip — a model written to DDL, applied to an empty
// schema and read again has to produce the same model. A difference there is a
// difference the structure diff would report between a database and an exact
// copy of it, which is the failure the whole product is judged on.
//
// The statements name nothing with its schema. Every identifier is written
// bare, and the script is meant for a session whose search path names the
// schema it is being applied to — which is what lets one script build the same
// structure under any name, and is what a sync between an environment and its
// copy needs. It is also the only coherent choice: the expressions the model
// carries are rendered by the server against the search path already, so
// qualifying the names around them would produce a statement that is half
// scoped to one schema and half to another. The catalog reads the same way,
// for the same reason.
//
// # What is taken on trust
//
// Some of what this package writes it did not compose. The definition of a
// constraint, of an index and of a view, and the default of a column, are text
// the server rendered and this writes back word for word — because rebuilding
// them would be a second implementation of a renderer that already exists, and
// a worse one. It is the choice pg_dump makes for the same reason.
//
// It is worth naming as a trust boundary rather than leaving as a technique. A
// desktop client connects wherever it is pointed, and a server that is not what
// it claims to be — or something on the wire pretending to be one — can answer
// anything at all to pg_get_constraintdef. What comes back is not escaped here
// and cannot be: it is SQL, and the whole point of keeping it is that it is SQL.
//
// So the guard is not in this package. It is that a script is read before it is
// run: the product's first rule is that nothing destructive happens without a
// preview, and this is one of the things that rule is protecting. Whatever
// applies a script should send it a statement at a time rather than as one
// block, so that what ran and what did not is known.
//
// What the model does not carry is not written and not guessed. A partitioned
// table written without its PARTITION BY is an ordinary table and a partition
// written without its bounds is a copy that holds the wrong rows, so both are
// declined by name instead — and so is anything that needs them, worked out
// from the dependency graph rather than left to fail on the target. ADR-0007
// requires an object that is not compared to be named as not compared; a Script
// carries that list beside its statements and prints it above them.
package ddl
