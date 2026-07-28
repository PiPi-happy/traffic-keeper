// Package web embeds the built frontend assets so the master binary serves
// the SPA without external static files.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var embedded embed.FS

// Dist returns the embedded frontend filesystem rooted at the dist directory.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Handler returns an http.Handler serving the embedded frontend.
func Handler() http.Handler {
	return http.FileServer(http.FS(Dist()))
}
