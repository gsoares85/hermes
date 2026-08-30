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
