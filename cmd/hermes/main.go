// Command hermes is the desktop entry point of the application.
package main

import (
	"flag"
	"fmt"

	"github.com/gsoares85/hermes/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the build information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Current())
		return
	}

	fmt.Println("hermes desktop")
}
