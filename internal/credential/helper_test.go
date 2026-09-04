package credential_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/gsoares85/hermes/internal/credential"
)

// The tests in this package need a real child process: a command line can only
// be read from the operating system, and a file can only be orphaned by a
// process that stopped without running any code of ours. The child is this test
// binary, re-executed into TestHelperProcess, which is the standard way to get
// one without shipping a second program.
const (
	helperMode      = "HERMES_CREDENTIAL_HELPER"
	helperDirectory = "HERMES_CREDENTIAL_HELPER_DIR"

	// reportCredential prints what the child was given, then waits.
	reportCredential = "report"
	// writePassfile writes a password file, then waits to be killed.
	writePassfile = "passfile"
	// waitForInterrupt installs the real signal handler, then waits to be
	// interrupted rather than killed.
	waitForInterrupt = "interrupt"
)

// credentialSeenByChild is what the child reports about its own environment.
type credentialSeenByChild struct {
	Password string `json:"password"`
	Passfile string `json:"passfile"`
}

// TestHelperProcess is not a test. It is the body of the child process the
// tests here start, and it does nothing at all when run on its own.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperMode)
	if mode == "" {
		t.Skip("this is the body of a child process, not a test")
	}

	switch mode {
	case reportCredential:
		report()
	case writePassfile:
		writeAndWait()
	case waitForInterrupt:
		watchAndWait()
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(2)
	}
}

// report tells the parent what reached this process, then waits, so that the
// parent can look at it from the outside while it is still running.
func report() {
	seen := credentialSeenByChild{
		Password: os.Getenv("PGPASSWORD"),
		Passfile: os.Getenv("PGPASSFILE"),
	}

	announce(seen)
	waitForParent()
}

// writeAndWait leaves a password file behind and never removes it. The parent
// kills this process outright, which is the point: nothing deferred here will
// ever run, and only the sweep can clean up after it.
func writeAndWait() {
	announce(writeHelperPassfile())
	waitForParent()
}

// watchAndWait installs the handler a binary installs, and then does nothing at
// all. Whether this process ever exits is up to that handler, which is the
// property under test.
func watchAndWait() {
	store := helperStore()

	stop := store.ReleaseOnInterrupt(context.Background())
	defer stop()

	announce("watching, " + writeHelperPassfile())
	waitForParent()
}

func helperStore() *credential.Store {
	return credential.NewStore(os.Getenv(helperDirectory))
}

func writeHelperPassfile() string {
	handoff, err := helperStore().InFile(credential.Target{
		Host:     "db.example.com",
		Port:     5432,
		Database: "app",
		User:     "reader",
		Password: password,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the password file: %v\n", err)
		os.Exit(3)
	}

	return handoff.String()
}

// announce writes the one line the parent reads before it acts on this process.
func announce(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		os.Exit(3)
	}
}

// waitForParent blocks until the parent closes the pipe, or kills this process.
func waitForParent() {
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// helper is the child process: prepared, then started, then read from.
type helper struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
}

// newHelper prepares the child. It is deliberately not started yet: a test
// applies a handoff to the command first, and that is the thing under test.
func newHelper(t *testing.T, mode string, environment ...string) *helper {
	t.Helper()

	command := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	command.Env = append(os.Environ(), append([]string{helperMode + "=" + mode}, environment...)...)
	command.Stderr = os.Stderr

	input, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("opening the input of the child: %v", err)
	}

	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the output of the child: %v", err)
	}

	return &helper{command: command, input: input, output: bufio.NewReader(output)}
}

func (h *helper) start(t *testing.T) {
	t.Helper()

	if err := h.command.Start(); err != nil {
		t.Fatalf("starting the child: %v", err)
	}

	t.Cleanup(func() {
		_ = h.input.Close()
		_ = h.command.Wait()
	})
}

// reported decodes the one line the child writes before it waits.
func (h *helper) reported(t *testing.T, into any) {
	t.Helper()

	line, err := h.output.ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading what the child reported: %v", err)
	}

	if err := json.Unmarshal(line, into); err != nil {
		t.Fatalf("reading what the child reported (%q): %v", line, err)
	}
}
