package server

import (
	"net/http"

	"github.com/criteo/consul-timeline/public"
)

const uiNotBuilt = `<!doctype html><meta charset="utf-8"><title>Consul Timeline</title>
<body style="font-family:system-ui;padding:40px;color:#333">
<h1>UI not built</h1>
<p>This binary was compiled without the web UI. Run <code>make ui</code> (needs Node) before <code>make release</code>,
or start the server with <code>-static-dir</code> pointing at a built UI.</p>
<p>The API is available under <code>/api/v1/</code>.</p>`

// static serves the web UI under /web/, from disk when configured for
// development and from the binary otherwise.
func (s *Server) static() http.Handler {
	if s.cfg.StaticDir != "" {
		return http.StripPrefix("/web/", http.FileServer(http.Dir(s.cfg.StaticDir)))
	}
	if !public.Built() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(uiNotBuilt))
		})
	}
	return http.StripPrefix("/web/", http.FileServer(http.FS(public.FS())))
}
