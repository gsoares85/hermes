// Command hermes is the desktop entry point of the application.
//
// It stays deliberately thin: it wires the frontend assets and the bound
// services into the Wails application and starts it. Everything that can be
// tested lives behind internal/, which knows nothing about this file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/gsoares85/hermes/frontend"
	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/credential"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/filestore"
	"github.com/gsoares85/hermes/internal/ui"
	"github.com/gsoares85/hermes/internal/vault"
	"github.com/gsoares85/hermes/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the build information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Current())
		return
	}

	if err := run(); err != nil {
		log.Fatalf("hermes: %v", err)
	}
}

// Level of the Wails system logger. Pinned rather than left to the framework
// default: at debug level Wails logs the arguments of every bound call, and the
// arguments of a connection call are a form with a password in it. The
// redaction below would catch it, but a secret that is never written is better
// than one that is written and then edited.
const logLevel = slog.LevelInfo

func run() error {
	// Every logger in this process, ours and the framework's, writes through
	// the redaction. Calling Redact at each log statement would be a
	// discipline, and a discipline is what fails the first time someone is in
	// a hurry; this makes logging a connection string with its password an
	// accident that cannot happen through this path.
	//
	// The handler underneath is the one the framework would have chosen for
	// this build, so a release binary keeps discarding its system log and a
	// development one keeps printing it — only redacted.
	logger := slog.New(secret.NewHandler(application.DefaultLogger(logLevel).Handler()))
	slog.SetDefault(logger)

	// Where passwords are kept is decided once, here, and the window is told
	// the answer rather than allowed to go looking: the dependency gate forbids
	// internal/ui from reaching for a keychain for the same reason it forbids
	// it from reaching for a driver.
	//
	// In the background, because asking costs more than the window has: a
	// Secret Service that has to be activated on the session bus, or a login
	// keychain that wants unlocking, can take longer to answer than the 1,5s
	// the whole cold start is allowed. The window is drawn first and asks
	// afterwards; nothing on screen needs the answer before then.
	//
	// Opening never fails. A machine with no keyring gets a vault that lives in
	// this process and a warning the window puts in front of the person, which
	// is the honest outcome — refusing to start over a convenience is not.
	opened := vault.OpenInBackground(context.Background())
	defer func() { _ = opened.Close() }()

	// The two layers of the promise that a temporary password file is removed
	// that belong to the program rather than to one operation: what an earlier
	// run left behind is swept, and what this run writes goes when the person
	// interrupts it. The third layer is the deferred release at each call site,
	// and none of the three has arrived at a subprocess yet — this is installed
	// before the first one does, not after.
	passwords, err := credential.DefaultDir()
	if err != nil {
		return err
	}

	releaseCredentials := credential.NewStore(passwords).Guard(context.Background())
	defer releaseCredentials()

	connections, err := filestore.ConnectionsPath()
	if err != nil {
		return err
	}

	app := application.New(application.Options{
		Name:        "Hermes",
		Description: "A native, open source database manager for PostgreSQL",
		Logger:      logger,
		LogLevel:    logLevel,
		Services: []application.Service{
			application.NewService(ui.NewAppInfoService()),
			// Every implementation is chosen here and nowhere else: the UI and
			// the core both program against the contracts, and this is the
			// outermost place that can name a driver, a keychain or a file.
			application.NewService(ui.NewConnectionService(ui.Dependencies{
				Opener:      postgres.New(),
				Store:       filestore.NewConnections(connections),
				Vault:       opened,
				VaultStatus: vaultStatus(opened),
			})),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontend.Dist()),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "Hermes",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,

		// Both fields are set on purpose. Left unset, the zero value is a fully
		// transparent black, which the webview takes literally: everything the
		// page has not painted itself shows the black window behind it, and the
		// window opens black.
		BackgroundType:   application.BackgroundTypeSolid,
		BackgroundColour: application.NewRGB(255, 255, 255),
	})

	if err := app.Run(); err != nil {
		return fmt.Errorf("running the application: %w", err)
	}

	return nil
}

// vaultStatus translates what the vault reports into what the window renders.
//
// The translation lives here and not in either package because neither should
// know the other: internal/vault answers in its own terms, internal/ui asks in
// its own, and wiring the two together is the job of the command.
func vaultStatus(opened *vault.Deferred) func(context.Context) (ui.VaultView, error) {
	return func(ctx context.Context) (ui.VaultView, error) {
		status, err := opened.Status(ctx)
		if err != nil {
			return ui.VaultView{}, err
		}

		return ui.VaultView{Backend: status.Backend, Warning: status.Warning}, nil
	}
}
