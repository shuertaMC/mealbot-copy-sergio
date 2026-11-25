// Package httpapi owns HTTP transport assembly for the API and public static
// assets.
package httpapi

import "net/http"

// StaticHandler serves tracked assets from a repository-relative directory.
func StaticHandler(directory string) http.Handler {
	if directory == "" {
		directory = "./static"
	}
	return http.FileServer(http.Dir(directory))
}

// RegisterStatic installs the public root file server after specific API
// patterns have been registered on mux.
func RegisterStatic(mux *http.ServeMux, directory string) {
	mux.Handle("/", StaticHandler(directory))
}
