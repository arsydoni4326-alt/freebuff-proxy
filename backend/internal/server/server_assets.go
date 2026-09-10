package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// mustSubFS returns the named subtree of an embed.FS. The directory is
// already validated at embed time, so this is a thin wrapper over
// fs.Sub for the rare case the embed changes shape.
func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("server: mustSubFS(%q): %v", dir, err))
	}
	return sub
}

// noDirListing rejects directory requests so FileServerFS never renders an
// index of the admin assets tree.
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}