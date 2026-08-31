// Package frontend embeds the built web assets into the binary.
//
// The Go package lives next to the web sources so that go:embed can reach the
// build output: embed only sees files under the package directory. Nothing else
// belongs here — this is a carrier, not a layer.
package frontend

import (
	"embed"
	"io/fs"
)

// The all: prefix is required, not incidental: dist/.gitkeep is tracked so that
// a fresh clone compiles before anyone runs npm, and without all: embed skips
// dotfiles and refuses a directory with nothing else in it.
//
// It also embeds whatever else happens to sit there, which is why `npm run
// build` now empties the directory first. Otherwise a sourcemap from a
// development build, or a stray .env, would travel inside a released binary.
//
//go:embed all:dist
var assets embed.FS

// Dist returns the built frontend, rooted at the directory that holds
// index.html.
func Dist() fs.FS {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		// Unreachable: the path is a constant checked at compile time by the
		// embed directive above.
		panic(err)
	}

	return dist
}
