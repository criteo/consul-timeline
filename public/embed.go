// Package public holds the built web UI, served under /web/. The files in
// dist/ come from `make ui` (Vite); the directory only contains .gitkeep in
// a fresh checkout so the package still compiles, and the server then
// explains how to build the UI instead of serving it.
package public

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS is the root of the built UI.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Built reports whether the UI was built into the binary.
func Built() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}
