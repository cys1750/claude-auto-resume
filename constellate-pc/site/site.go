// Package site holds the Constellate web app. It is staged here by fetch-web.sh
// rather than committed, and compiled into both the launcher and the bundler —
// which is why it lives in a package of its own: go:embed cannot reach outside
// the directory of the package that declares it.
package site

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var FS embed.FS

// Paths within FS, for the bundler which needs the two files individually.
const (
	AppFile   = "web/index.html"
	ThreeFile = "web/vendor/three.min.js"
)

// Files returns the app rooted at its own directory, for serving.
func Files() (fs.FS, error) {
	return fs.Sub(FS, "web")
}
