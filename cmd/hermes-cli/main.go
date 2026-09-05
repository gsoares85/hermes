// Command hermes-cli runs Hermes profiles headlessly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/credential"
	"github.com/gsoares85/hermes/internal/version"
)

// The exit status of a command line that was not understood, and of one that
// failed before it could start.
const (
	statusUsage  = 2
	statusFailed = 1
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

	// os.Exit skips every deferred call, so the status leaves through here and
	// nowhere below: a run that exits from inside run would take the removal of
	// its own password files with it.
	os.Exit(run())
}

func run() int {
	// The same guard the window installs. Headless is where the temporary
	// password file actually gets used — an operation across two servers is a
	// profile, and a profile runs here.
	passwords, err := credential.DefaultDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hermes-cli: %v\n", err)

		return statusFailed
	}

	releaseCredentials := credential.NewStore(passwords).Guard(context.Background())
	defer releaseCredentials()

	fmt.Fprintln(os.Stderr, "usage: hermes-cli run <profile.toml>")
	fmt.Fprintln(os.Stderr, "running profiles headlessly is not implemented yet")

	return statusUsage
}
