// Package profile turns the things a person configures — the connections they
// saved, and later the operations they scripted — into TOML and back.
//
// It serializes and validates; it does not decide where a file lives or touch
// one. Paths, permissions and directories are infrastructure and stay outside
// the core, which is also what lets every format decision here be tested
// against bytes rather than against a filesystem.
//
// It belongs to the core layer: it must never import UI or
// framework packages. See docs of the architecture for the dependency rule.
package profile
