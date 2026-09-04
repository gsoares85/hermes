// Command hermes-cli runs Hermes profiles headlessly.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/version"
)

func main() {
	// The same redaction the window installs, for the same reason. Headless is
	// where a connection string is most likely to be written down: this output
	// is what ends up in a CI log, in a scrollback and in a bug report.
	slog.SetDefault(slog.New(secret.NewHandler(slog.NewTextHandler(os.Stderr, nil))))

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
