// Package web holds the dashboard's built interface.
//
// The interface is compiled into the binary rather than installed alongside it,
// so a deployment is one file. There is no directory of assets to keep in step
// with the executable and nothing to serve from the wrong version by accident.
package web

import (
	"embed"
	"io/fs"
)

// dist is whatever `pnpm build` last produced.
//
// The all: prefix matters. Vite emits an assets directory, and a plain embed
// would silently skip anything beginning with a dot or an underscore, which is
// the kind of omission that only shows up as one missing file in production.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the built interface, rooted so paths look like URLs.
func Assets() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}

// Built reports whether an interface was compiled in.
//
// A binary can be built without running the frontend build, which is convenient
// while working on core and confusing to meet by accident. Knowing lets the
// dashboard say so plainly instead of answering every request with a 404.
func Built() bool {
	assets, err := Assets()
	if err != nil {
		return false
	}
	_, err = fs.Stat(assets, "index.html")
	return err == nil
}
