// Package catalog reads pg_catalog and answers a normalised model of a schema.
//
// It is the foundation of three things that come after it: the object tree, the
// generated DDL, and — the one that decides whether the product is trusted — the
// structure diff. A diff is only worth showing if comparing a schema against
// itself produces nothing, and that property is not won in the diff. It is won
// here, in how faithfully two readings of equivalent schemas produce equal
// models.
//
// Hence the shape of this package. The model is made of types with
// constructors rather than of strings, so that a value which skipped
// normalisation is a value that cannot be built. What the catalog returns in
// more than one spelling — int4 and integer, timestamptz and timestamp with
// time zone — is folded to one before it is stored, not compared leniently
// afterwards. Every list comes out in a stable, explicit order, because a
// model whose order depends on Go's map iteration compares unequal to itself.
//
// It reads pg_catalog and never information_schema: the portable view is
// slower and does not expose partial indexes, exclusion constraints or storage
// parameters, which are exactly the things a diff has to see. See ADR-0007.
package catalog
