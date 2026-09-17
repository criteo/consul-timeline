// Package server exposes the timeline over HTTP: a JSON API, a live event
// stream, the embedded web UI, and the operational endpoints.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/criteo/consul-timeline/storage"
)

// Watch is what the server needs to know about the watcher.
type Watch interface {
	Datacenter() string
	Ready() <-chan struct{}
	ServiceNames() []string
	NodeNames() []string
}

type Server struct {
	cfg           Config
	store         storage.Storage
	watch         Watch
	hub           *Hub
	version       string
	retentionDays int
	mux           *http.ServeMux
}

// New builds the server; retentionDays is reported by /api/v1/meta so the
// UI knows how far back history goes.
func New(cfg Config, store storage.Storage, watch Watch, hub *Hub, version string, retentionDays int) *Server {
	s := &Server{cfg: cfg, store: store, watch: watch, hub: hub, version: version, retentionDays: retentionDays}
	s.routes()
	return s
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/web/", http.StatusMovedPermanently)
	})

	m.HandleFunc("GET /api/v1/meta", s.handleMeta)
	m.HandleFunc("GET /api/v1/events", s.handleEvents)
	m.HandleFunc("GET /api/v1/histogram", s.handleHistogram)
	m.HandleFunc("GET /api/v1/facets", s.handleFacets)
	m.HandleFunc("GET /api/v1/suggest", s.handleSuggest)
	m.HandleFunc("GET /api/v1/instance", s.handleInstance)
	m.HandleFunc("GET /api/v1/stream", s.handleStream)

	// endpoints of the previous UI, kept until it is replaced
	m.HandleFunc("GET /events", s.handleLegacyEvents)
	m.HandleFunc("GET /filter-entries", s.handleLegacyFilterEntries)

	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("OK")) })
	m.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("OK")) })
	m.HandleFunc("GET /readyz", s.handleReady)
	m.Handle("GET /metrics", promhttp.Handler())
	m.HandleFunc("GET /debug/pprof/", pprof.Index)
	m.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	m.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	m.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	m.HandleFunc("GET /debug/pprof/trace", pprof.Trace)

	m.Handle("GET /web/", s.static())
	s.mux = m
}

// Handler is the full HTTP handler: gzip everywhere except the stream,
// which flushes small frames continuously.
func (s *Server) Handler() http.Handler {
	gz := gziphandler.GzipHandler(s.mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/stream") {
			s.mux.ServeHTTP(w, r)
			return
		}
		gz.ServeHTTP(w, r)
	})
}

// Run serves until ctx is cancelled, then drains connections.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		s.hub.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	slog.Info("http: listening", "addr", s.cfg.ListenAddr)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.watch.Ready():
	default:
		http.Error(w, "watcher not ready", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.store.Datacenters(ctx); err != nil {
		http.Error(w, "storage: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("OK"))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("http: encoding response", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
