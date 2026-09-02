// Command hermes-cli runs Hermes profiles headlessly.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gsoares85/hermes/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the build information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Current())
		return
	}

	fmt.Fprintln(os.Stderr, "usage: hermes-cli run <profile.toml>")
	fmt.Fprintln(os.Stderr, "running profiles headlessly is not implemented yet")
	os.Exit(2)
}
