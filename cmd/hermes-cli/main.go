// Command hermes-cli runs Hermes profiles headlessly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/core/store"
	"github.com/gsoares85/hermes/internal/credential"
	"github.com/gsoares85/hermes/internal/sqlitestore"
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

	if flag.Arg(0) == "jobs" {
		return jobs(context.Background())
	}

	fmt.Fprintln(os.Stderr, "usage: hermes-cli run <profile.toml>")
	fmt.Fprintln(os.Stderr, "       hermes-cli jobs")
	fmt.Fprintln(os.Stderr, "running profiles headlessly is not implemented yet")

	return statusUsage
}

// jobs prints what has already run, newest first.
//
// The same history the window draws its panel from, and the reason this
// command exists before there is anything headless to put in it: a pipeline
// that ran a profile overnight asks what happened from a terminal, not from a
// desktop application. It is also what makes the local database part of this
// binary rather than a claim about it — which is what the build with cgo
// switched off is there to keep true.
func jobs(ctx context.Context) int {
	path, err := sqlitestore.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hermes-cli: %v\n", err)

		return statusFailed
	}

	opened, err := sqlitestore.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hermes-cli: %v\n", err)

		return statusFailed
	}
	defer func() { _ = opened.Close() }()

	// One page, like the panel. Somebody who wants the whole of a year of
	// history wants a file, and this is a command for looking at last night.
	held, err := opened.Jobs().Recent(ctx, store.Page{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "hermes-cli: %v\n", err)

		return statusFailed
	}

	if len(held) == 0 {
		fmt.Fprintln(os.Stderr, "no jobs have run yet")

		return 0
	}

	// Aligned rather than separated, because this is read by a person. A
	// machine-readable shape is a flag somebody will ask for, and inventing
	// one before they do would be inventing the format twice.
	out := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, record := range held {
		// A line that will not write is a pipe somebody closed — this piped
		// into head. The flush below reports anything worth reporting.
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n",
			record.Ended.Local().Format("2006-01-02 15:04"),
			record.State, record.Kind, record.Title)
	}

	if err := out.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "hermes-cli: %v\n", err)

		return statusFailed
	}

	return 0
}
