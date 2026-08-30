// Command nextversion prints the version that follows the last released tag.
//
// The release workflow calls it with the previous tag and the increment taken
// from the pull request label. The arithmetic itself lives in internal/version,
// where it is covered by tests; this is only the command line around it.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gsoares85/hermes/internal/version"
)

func main() {
	last := flag.String("last", "", "last released tag, empty when there is none yet")
	bump := flag.String("bump", "", "increment to apply: patch, minor or major")
	flag.Parse()

	parsedBump, err := version.ParseBump(*bump)
	if err != nil {
		fail(err)
	}

	next, err := version.Next(*last, parsedBump)
	if err != nil {
		fail(err)
	}

	fmt.Println(next)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "nextversion: %v\n", err)
	os.Exit(1)
}
