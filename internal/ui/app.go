// Package ui is the boundary with the Wails framework: it holds the services
// bound to the frontend and nothing else.
//
// Two rules govern everything exposed from here. The frontend never assembles
// SQL — statements are produced in Go, where they are testable. And no secret
// crosses the boundary: the frontend receives state, never a credential.
package ui

import "github.com/gsoares85/hermes/internal/version"

// AppInfoService exposes the build information of the running binary.
//
// It is the reference shape for every service that follows: it takes no input,
// returns data ready to render, and carries nothing sensitive.
type AppInfoService struct{}

// NewAppInfoService creates the service bound to the frontend.
func NewAppInfoService() *AppInfoService {
	return &AppInfoService{}
}

// Info returns the version, commit, build date and platform of this build.
func (s *AppInfoService) Info() version.Info {
	return version.Current()
}
