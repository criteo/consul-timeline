package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

const (
	clientBuffer  = 256
	replayLimit   = 1000
	pingInterval  = 15 * time.Second
	streamTimeout = 2 * time.Second
)

// Hub fans live events out to stream clients, each with its own filter.
// A client that cannot keep up is disconnected; it reconnects with the
// id of the last event it saw and the server replays the gap from storage.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	closed  bool
}

type client struct {
	ch       chan tl.Event
	q        storage.Query
	overflow atomic.Bool
}

func NewHub() *Hub {
	return &Hub{clients: map[*client]struct{}{}}
}

// Publish delivers e to every client whose filter matches, never blocking.
func (h *Hub) Publish(e tl.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for c := range h.clients {
		if !storage.Match(e, c.q) {
			continue
		}
		select {
		case c.ch <- e:
		default:
			c.overflow.Store(true)
		}
	}
}

func (h *Hub) subscribe(q storage.Query) *client {
	c := &client{ch: make(chan tl.Event, clientBuffer), q: q}
	h.mu.Lock()
	if h.closed {
		close(c.ch)
	} else {
		h.clients[c] = struct{}{}
	}
	h.mu.Unlock()
	return c
}

func (h *Hub) unsubscribe(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Close ends every stream; used at shutdown so Server.Run can drain.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for c := range h.clients {
		close(c.ch)
		delete(h.clients, c)
	}
}

// Clients is the number of connected streams.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// handleStream serves Server-Sent Events for the local datacenter. Each
// frame carries the event time in milliseconds as its id; a reconnecting
// client sends it back as Last-Event-ID (or ?since=) and gets the events
// it missed first, from storage, then the live feed.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	local := s.watch.Datacenter()
	if q.Datacenter == "" {
		q.Datacenter = local
	}
	if q.Datacenter != local {
		writeError(w, http.StatusBadRequest, fmt.Errorf("live stream is only available for %s on this instance", local))
		return
	}
	q.From, q.To, q.Cursor, q.Limit = time.Time{}, time.Time{}, storage.Cursor{}, 0

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}

	since, err := parseTime(r.Header.Get("Last-Event-ID"))
	if err != nil || since.IsZero() {
		since, _ = parseTime(r.URL.Query().Get("since"))
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	c := s.hub.subscribe(q)
	defer s.hub.unsubscribe(c)

	if !since.IsZero() {
		missed, err := s.store.Since(r.Context(), q.Datacenter, since, replayLimit)
		if err != nil {
			writeFrame(w, "error", map[string]string{"error": err.Error()})
		}
		for _, e := range missed {
			if storage.Match(e, q) {
				writeEvent(w, e)
			}
		}
		if len(missed) >= replayLimit {
			writeFrame(w, "gap", map[string]string{"error": "more events were missed than could be replayed"})
		}
	}
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-c.ch:
			if !ok {
				return
			}
			writeEvent(w, e)
			if c.overflow.Load() {
				writeFrame(w, "reset", map[string]string{"error": "client too slow, reconnect to replay"})
				flusher.Flush()
				return
			}
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeEvent(w http.ResponseWriter, e tl.Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\nevent: event\ndata: %s\n\n", e.Time.UnixMilli(), b)
}

func writeFrame(w http.ResponseWriter, event string, v any) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
