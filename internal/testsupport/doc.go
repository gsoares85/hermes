// Package testsupport holds the fixtures shared by integration tests.
//
// Everything here is behind the `integration` build tag: these helpers start
// real PostgreSQL containers, so they must never be reachable from a unit test
// run, and they contribute no statements to the coverage floor.
package testsupport
