// Package conn owns connection pooling, sessions and the secret vault.
//
// It belongs to the core layer: it must never import UI or
// framework packages. See docs of the architecture for the dependency rule.
package conn
