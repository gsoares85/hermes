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

func run() error {
	// Where passwords are kept is decided once, here, and the window is told
	// the answer rather than allowed to go looking: the dependency gate forbids
	// internal/ui from reaching for a keychain for the same reason it forbids
	// it from reaching for a driver.
	//
	// Open never fails. A machine with no keyring gets a vault that lives in
	// this process and a warning the window puts in front of the person, which
	// is the honest outcome — refusing to start over a convenience is not.
	opened, status := vault.Open(context.Background())
	defer func() { _ = opened.Close() }()

	connections, err := filestore.ConnectionsPath()
	if err != nil {
		return err
	}

	app := application.New(application.Options{
		Name:        "Hermes",
		Description: "A native, open source database manager for PostgreSQL",
		// Pinned rather than left to the framework default. At debug level
		// Wails logs the arguments of every bound call, and the arguments of
		// a connection call are a form with a password in it. The level a
		// secret depends on is a level this application chooses.
		LogLevel: slog.LevelInfo,
		Services: []application.Service{
			application.NewService(ui.NewAppInfoService()),
			// Every implementation is chosen here and nowhere else: the UI and
			// the core both program against the contracts, and this is the
			// outermost place that can name a driver, a keychain or a file.
			application.NewService(ui.NewConnectionService(ui.Dependencies{
				Opener:      postgres.New(),
				Store:       filestore.NewConnections(connections),
				Vault:       opened,
				VaultStatus: ui.VaultView{Backend: status.Backend, Warning: status.Warning},
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
