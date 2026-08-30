// Command hermes is the desktop entry point of the application.
//
// It stays deliberately thin: it wires the frontend assets and the bound
// services into the Wails application and starts it. Everything that can be
// tested lives behind internal/, which knows nothing about this file.
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/gsoares85/hermes/frontend"
	"github.com/gsoares85/hermes/internal/ui"
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
	app := application.New(application.Options{
		Name:        "Hermes",
		Description: "A native, open source database manager for PostgreSQL",
		Services: []application.Service{
			application.NewService(ui.NewAppInfoService()),
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
	})

	if err := app.Run(); err != nil {
		return fmt.Errorf("running the application: %w", err)
	}

	return nil
}
