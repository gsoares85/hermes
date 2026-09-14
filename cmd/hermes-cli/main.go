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
		return jobs(context.Background(), flag.Args()[1:])
	}

	fmt.Fprintln(os.Stderr, "usage: hermes-cli run <profile.toml>")
	fmt.Fprintln(os.Stderr, "       hermes-cli jobs [-n count]")
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
func jobs(ctx context.Context, args []string) int {
	asked := flag.NewFlagSet("jobs", flag.ContinueOnError)
	count := asked.Int("n", store.DefaultPageSize,
		"how many jobs to print, newest first")

	if err := asked.Parse(args); err != nil {
		return statusUsage
	}

	// Nothing to print is not the same as nothing to say. Asked for no jobs,
	// the reading below finds none and the message underneath reports that the
	// history is empty — a false statement about the machine, with a status
	// that says the command worked.
	if *count < 1 {
		fmt.Fprintln(os.Stderr, "hermes-cli: -n is how many jobs to print, so it must be at least 1")

		return statusUsage
	}

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

	held, err := recent(ctx, opened.Jobs(), *count)
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

// recent reads as many of the newest jobs as were asked for, a page at a time.
//
// By the row each page ended on rather than by how many to skip, which is how
// the store answers and the only shape that stays right while jobs are ending:
// counting from the start would show one row twice and miss the one after it.
// Somebody asking for more than there is gets what there is.
func recent(ctx context.Context, history store.JobHistory, count int) ([]store.JobRecord, error) {
	var found []store.JobRecord

	page := store.Page{}
	for len(found) < count {
		page.Limit = count - len(found)

		read, err := history.Recent(ctx, page)
		if err != nil {
			return nil, err
		}

		found = append(found, read...)

		// A page shorter than the one asked for is the end of the history.
		if len(read) < page.Size() {
			break
		}

		page.After = read[len(read)-1].Cursor()
	}

	return found, nil
}
