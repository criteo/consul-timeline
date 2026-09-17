package server

import (
	"net/http"

	"github.com/criteo/consul-timeline/public"
)

// static serves the web UI under /web/, from disk when configured for
// development and from the binary otherwise.
func (s *Server) static() http.Handler {
	var fsys http.FileSystem
	if s.cfg.StaticDir != "" {
		fsys = http.Dir(s.cfg.StaticDir)
	} else {
		fsys = http.FS(public.FS)
	}
	return http.StripPrefix("/web/", http.FileServer(fsys))
}
