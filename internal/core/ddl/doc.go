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
// What the model does not carry is not written and not guessed. A partitioned
// table written without its PARTITION BY is an ordinary table and a partition
// written without its bounds is a copy that holds the wrong rows, so both are
// declined by name instead — and so is anything that needs them, worked out
// from the dependency graph rather than left to fail on the target. ADR-0007
// requires an object that is not compared to be named as not compared; a Script
// carries that list beside its statements and prints it above them.
package ddl
