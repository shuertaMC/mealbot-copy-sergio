// Package httpapi owns HTTP transport assembly. During the compatibility
// checkpoint the legacy API handlers remain behind a bridge while static asset
// serving is already owned here.
package httpapi

import "net/http"

// StaticHandler serves tracked assets from a repository-relative directory.
func StaticHandler(directory string) http.Handler {
	return http.FileServer(http.Dir(directory))
}

// RegisterStatic installs the public root file server after specific API
// patterns have been registered on mux.
func RegisterStatic(mux *http.ServeMux, directory string) {
	mux.Handle("/", StaticHandler(directory))
}
