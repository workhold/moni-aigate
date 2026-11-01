// Package web contains the embedded console. Run npm ci && npm run build in web
// before creating a release binary. An unbuilt checkout serves a clear setup page.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist static/index.html
var content embed.FS

// FS exposes the built application at its root, or the source checkout fallback.
var FS fs.FS = consoleFS()

func consoleFS() fs.FS {
	built, err := fs.Sub(content, "dist")
	if err == nil {
		if _, err := fs.Stat(built, "index.html"); err == nil {
			return built
		}
	}
	fallback, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return fallback
}
