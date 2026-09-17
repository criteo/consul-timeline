package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

// parseQuery reads the common query parameters:
//
//	dc      datacenter, "all" or empty for every datacenter
//	from,to RFC 3339, unix seconds or milliseconds
//	f       filter "field:value", repeatable, "-" prefix excludes; same
//	        field and sign accumulate as alternatives
//	q       free text over output and names
//	cursor  continuation from a previous page
//	limit   page size
func parseQuery(r *http.Request) (storage.Query, error) {
	v := r.URL.Query()
	q := storage.Query{Datacenter: v.Get("dc"), Text: strings.TrimSpace(v.Get("q"))}
	if q.Datacenter == "all" {
		q.Datacenter = ""
	}
	var err error
	if q.From, err = parseTime(v.Get("from")); err != nil {
		return q, fmt.Errorf("from: %w", err)
	}
	if q.To, err = parseTime(v.Get("to")); err != nil {
		return q, fmt.Errorf("to: %w", err)
	}
	if q.Cursor, err = storage.ParseCursor(v.Get("cursor")); err != nil {
		return q, err
	}
	if l := v.Get("limit"); l != "" {
		if q.Limit, err = strconv.Atoi(l); err != nil {
			return q, errors.New("limit must be a number")
		}
	}
	for _, raw := range v["f"] {
		f, err := parseFilter(raw)
		if err != nil {
			return q, err
		}
		merged := false
		for i := range q.Filters {
			if q.Filters[i].Field == f.Field && q.Filters[i].Not == f.Not {
				q.Filters[i].Values = append(q.Filters[i].Values, f.Values...)
				merged = true
				break
			}
		}
		if !merged {
			q.Filters = append(q.Filters, f)
		}
	}
	return q, q.Validate()
}

func parseFilter(raw string) (storage.Filter, error) {
	raw = strings.TrimSpace(raw)
	f := storage.Filter{}
	if strings.HasPrefix(raw, "-") {
		f.Not = true
		raw = raw[1:]
	}
	field, value, ok := strings.Cut(raw, ":")
	if !ok || field == "" || value == "" {
		return f, fmt.Errorf("filter %q must be field:value", raw)
	}
	f.Field = strings.ToLower(field)
	f.Values = []string{value}
	return f, nil
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "now":
		if s == "now" {
			return time.Now(), nil
		}
		return time.Time{}, nil
	case isDigits(s):
		n, _ := strconv.ParseInt(s, 10, 64)
		if n > 1e12 {
			return time.UnixMilli(n), nil
		}
		return time.Unix(n, 0), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not RFC 3339 or a unix timestamp", s)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func intParam(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ---- handlers ---------------------------------------------------------------

type metaResponse struct {
	LocalDatacenter string   `json:"local_dc"`
	Datacenters     []string `json:"datacenters"`
	RetentionDays   int      `json:"retention_days"`
	Version         string   `json:"version"`
	Fields          []string `json:"fields"`
	FacetFields     []string `json:"facet_fields"`
	Kinds           []string `json:"kinds"`
	Statuses        []string `json:"statuses"`
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	dcs, err := s.store.Datacenters(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	local := s.watch.Datacenter()
	if local != "" {
		found := false
		for _, dc := range dcs {
			found = found || dc == local
		}
		if !found {
			dcs = append(dcs, local)
		}
	}
	sort.Strings(dcs)
	res := metaResponse{LocalDatacenter: local, Datacenters: dcs, RetentionDays: s.retentionDays, Version: s.version, Fields: storage.Fields, FacetFields: storage.FacetFields}
	for k := tl.KindCheck; k <= tl.KindNode; k++ {
		res.Kinds = append(res.Kinds, k.String())
	}
	for st := tl.StatusMissing; st <= tl.StatusMaintenance; st++ {
		res.Statuses = append(res.Statuses, st.String())
	}
	writeJSON(w, http.StatusOK, res)
}

type eventsResponse struct {
	Events     []tl.Event `json:"events"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	page, err := s.store.Events(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if page.Events == nil {
		page.Events = []tl.Event{}
	}
	writeJSON(w, http.StatusOK, eventsResponse{Events: page.Events, NextCursor: page.Next.String(), HasMore: page.HasMore})
}

type bucketJSON struct {
	Time  time.Time      `json:"t"`
	Total int            `json:"total"`
	By    map[string]int `json:"by"` // status names or datacenters, see Split
}

type histogramResponse struct {
	BucketSeconds float64       `json:"bucket_seconds"`
	Sampled       bool          `json:"sampled"`
	Split         storage.Split `json:"split"`
	Buckets       []bucketJSON  `json:"buckets"`
}

func (s *Server) handleHistogram(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	split, err := storage.ParseSplit(r.URL.Query().Get("split"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	buckets, sampled, err := s.store.Histogram(r.Context(), q, intParam(r, "buckets", 60), split)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	res := histogramResponse{Sampled: sampled, Split: split, Buckets: make([]bucketJSON, 0, len(buckets))}
	if len(buckets) > 1 {
		res.BucketSeconds = buckets[1].Start.Sub(buckets[0].Start).Seconds()
	}
	for _, b := range buckets {
		bj := bucketJSON{Time: b.Start, Total: b.Total, By: b.By}
		if bj.By == nil {
			bj.By = map[string]int{}
		}
		res.Buckets = append(res.Buckets, bj)
	}
	writeJSON(w, http.StatusOK, res)
}

type facetsResponse struct {
	SampleSize int                             `json:"sample_size"`
	Sampled    bool                            `json:"sampled"`
	Facets     map[string][]storage.FacetValue `json:"facets"`
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fields := storage.FacetFields
	if f := r.URL.Query().Get("fields"); f != "" {
		fields = strings.Split(f, ",")
	}
	facets, err := s.store.Facets(r.Context(), q, fields, intParam(r, "limit", 10))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	for _, f := range fields {
		if facets.Values[f] == nil {
			facets.Values[f] = []storage.FacetValue{}
		}
	}
	writeJSON(w, http.StatusOK, facetsResponse{SampleSize: facets.SampleSize, Sampled: facets.Sampled, Facets: facets.Values})
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	dc, field, prefix := v.Get("dc"), v.Get("field"), v.Get("q")
	if dc == "all" {
		dc = ""
	}
	limit := intParam(r, "limit", 20)
	values, err := s.store.Suggest(r.Context(), dc, field, prefix, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// what is registered right now may not have produced a stored event yet
	if dc == "" || dc == s.watch.Datacenter() {
		var live []string
		switch field {
		case storage.FieldService:
			live = s.watch.ServiceNames()
		case storage.FieldNode:
			live = s.watch.NodeNames()
		}
		for _, n := range live {
			if strings.HasPrefix(n, prefix) {
				values = append(values, n)
			}
		}
	}
	sort.Strings(values)
	out := make([]string, 0, len(values))
	for i, n := range values {
		if i == 0 || n != values[i-1] {
			out = append(out, n)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, map[string][]string{"values": out})
}

func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	in, err := s.store.Instance(r.Context(), v.Get("dc"), v.Get("node"), v.Get("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if in == nil {
		writeError(w, http.StatusNotFound, errors.New("unknown instance"))
		return
	}
	writeJSON(w, http.StatusOK, in)
}
