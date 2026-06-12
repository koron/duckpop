// Package fileserver provides an HTTP file server that allows not only reading
// but also creating and deleting directories and files.
package fileserver

import (
	"net/http"

	"golang.org/x/net/webdav"
)

type Server struct {
	dir    string
	normal http.Handler
	webdav *webdav.Handler
}

func New(dir string) *Server {
	return &Server{
		dir:    dir,
		normal: newListDirHandler(dir),
		webdav: &webdav.Handler{
			FileSystem: webdav.Dir(dir),
			LockSystem: webdav.NewMemLS(),
		},
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET", "HEAD":
		s.normal.ServeHTTP(w, r)
	default:
		s.webdav.ServeHTTP(w, r)
	}
}
