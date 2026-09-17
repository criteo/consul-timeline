package server

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

// legacyEvent is the JSON shape served by versions before 0.3, which the
// previous UI still expects.
type legacyEvent struct {
	Time             time.Time `json:"time"`
	Datacenter       string    `json:"datacenter"`
	NodeName         string    `json:"node_name,omitempty"`
	NodeIP           string    `json:"node_ip,omitempty"`
	OldNodeStatus    tl.Status `json:"old_node_status"`
	NewNodeStatus    tl.Status `json:"new_node_status"`
	ServiceName      string    `json:"service_name,omitempty"`
	ServiceID        string    `json:"service_id,omitempty"`
	OldServiceStatus tl.Status `json:"old_service_status"`
	NewServiceStatus tl.Status `json:"new_service_status"`
	OldInstanceCount int       `json:"old_instance_count"`
	NewInstanceCount int       `json:"new_instance_count"`
	CheckName        string    `json:"check_name,omitempty"`
	OldCheckStatus   tl.Status `json:"old_check_status"`
	NewCheckStatus   tl.Status `json:"new_check_status"`
	CheckOutput      string    `json:"check_output,omitempty"`
}

func toLegacy(e tl.Event) legacyEvent {
	return legacyEvent{
		Time: e.Time, Datacenter: e.Datacenter, NodeName: e.NodeName, NodeIP: e.NodeIP,
		OldNodeStatus: e.OldNodeStatus, NewNodeStatus: e.NewNodeStatus,
		ServiceName: e.ServiceName, ServiceID: e.ServiceID, OldServiceStatus: e.OldServiceStatus, NewServiceStatus: e.NewServiceStatus,
		OldInstanceCount: e.OldHealthy, NewInstanceCount: e.NewHealthy,
		CheckName: e.CheckName, OldCheckStatus: e.OldCheckStatus, NewCheckStatus: e.NewCheckStatus, CheckOutput: e.CheckOutput,
	}
}

// handleLegacyEvents serves GET /events?start=<unix>&filter=<name>&limit=n
// for the previous UI. The filter matched a service or a node name; the
// service reading is tried first.
func (s *Server) handleLegacyEvents(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := storage.Query{Datacenter: s.watch.Datacenter(), Limit: 100}
	if st := v.Get("start"); st != "" {
		ts, err := strconv.ParseInt(st, 10, 64)
		if err != nil {
			http.Error(w, "error parsing start filter", http.StatusBadRequest)
			return
		}
		q.To = time.Unix(ts, 0)
	}
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil {
			http.Error(w, "error parsing limit filter", http.StatusBadRequest)
			return
		}
		q.Limit = n
	}
	name := v.Get("filter")
	fields := []string{""}
	if name != "" {
		fields = []string{storage.FieldService, storage.FieldNode}
	}
	var events []tl.Event
	for _, field := range fields {
		q.Filters = nil
		if field != "" {
			q.Filters = []storage.Filter{{Field: field, Values: []string{name}}}
		}
		page, err := s.store.Events(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		events = page.Events
		if len(events) > 0 {
			break
		}
	}
	out := make([]legacyEvent, 0, len(events))
	for _, e := range events {
		out = append(out, toLegacy(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLegacyFilterEntries(w http.ResponseWriter, r *http.Request) {
	names := append(s.watch.ServiceNames(), s.watch.NodeNames()...)
	sort.Strings(names)
	writeJSON(w, http.StatusOK, names)
}
