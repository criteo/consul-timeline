// Package memory keeps the most recent events in a ring buffer. It is the
// storage for development and tests, and for running without a database.
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

var _ storage.Storage = (*Storage)(nil)

type instanceKey struct {
	dc, node, id string
}

type Storage struct {
	mu        sync.RWMutex
	events    []tl.Event
	next      int
	size      int
	nextID    int64
	instances map[instanceKey]tl.Instance
}

func New(cfg Config) *Storage {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = DefaultConfig.MaxSize
	}
	return &Storage{events: make([]tl.Event, cfg.MaxSize), instances: map[instanceKey]tl.Instance{}}
}

func (s *Storage) StoreEvents(_ context.Context, events []tl.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		s.nextID++
		e.ID = s.nextID
		s.events[s.next] = e
		s.next = (s.next + 1) % len(s.events)
		if s.size < len(s.events) {
			s.size++
			sizeGauge.Set(float64(s.size))
		}
	}
	return nil
}

func (s *Storage) UpsertInstances(_ context.Context, instances []tl.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range instances {
		k := instanceKey{in.Datacenter, in.NodeName, in.ServiceID}
		if old, ok := s.instances[k]; ok {
			in.FirstSeen = old.FirstSeen
		}
		s.instances[k] = in
	}
	return nil
}

func (s *Storage) Maintain(context.Context) error { return nil }

// matching returns the stored events satisfying q, newest first.
func (s *Storage) matching(q storage.Query) []tl.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]tl.Event, 0, s.size)
	for j := 0; j < s.size; j++ {
		e := s.events[(s.next-1-j+len(s.events))%len(s.events)]
		if storage.Match(e, q) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.After(out[j].Time)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func (s *Storage) Events(_ context.Context, q storage.Query) (storage.Page, error) {
	if err := q.Validate(); err != nil {
		return storage.Page{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	all := s.matching(q)
	if !q.Cursor.IsZero() {
		i := sort.Search(len(all), func(i int) bool {
			e := all[i]
			return e.Time.Before(q.Cursor.Time) || (e.Time.Equal(q.Cursor.Time) && e.ID < q.Cursor.ID)
		})
		all = all[i:]
	}
	page := storage.Page{}
	if len(all) > limit {
		page.Events = all[:limit]
		last := page.Events[limit-1]
		page.Next = storage.Cursor{Time: last.Time, ID: last.ID}
		page.HasMore = true
	} else {
		page.Events = all
	}
	return page, nil
}

func (s *Storage) Histogram(_ context.Context, q storage.Query, buckets int, split storage.Split) ([]storage.Bucket, bool, error) {
	if err := q.Validate(); err != nil {
		return nil, false, err
	}
	to := q.To
	if to.IsZero() {
		to = time.Now()
	}
	from := q.From
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}
	if buckets <= 0 {
		buckets = 60
	}
	span := to.Sub(from)
	if span <= 0 {
		return nil, false, nil
	}
	width := ((span + time.Duration(buckets) - 1) / time.Duration(buckets)).Truncate(time.Second)
	if width < time.Second {
		width = time.Second
	}
	n := int((span + width - 1) / width)
	out := make([]storage.Bucket, n)
	for i := range out {
		out[i] = storage.Bucket{Start: from.Add(time.Duration(i) * width), By: map[string]int{}}
	}
	for _, e := range s.matching(q) {
		i := int(e.Time.Sub(from) / width)
		if i >= 0 && i < n {
			out[i].Total++
			out[i].By[storage.BucketKey(e, split)]++
		}
	}
	return out, false, nil
}

func (s *Storage) Facets(_ context.Context, q storage.Query, fields []string, limit int) (storage.Facets, error) {
	if err := q.Validate(); err != nil {
		return storage.Facets{}, err
	}
	if limit <= 0 {
		limit = 10
	}
	all := s.matching(q)
	res := storage.Facets{Values: map[string][]storage.FacetValue{}, SampleSize: len(all)}
	for _, f := range fields {
		counts := map[string]int{}
		for _, e := range all {
			for _, v := range storage.FieldValues(e, f) {
				counts[v]++
			}
		}
		vals := make([]storage.FacetValue, 0, len(counts))
		for v, c := range counts {
			vals = append(vals, storage.FacetValue{Value: v, Count: c})
		}
		sort.Slice(vals, func(i, j int) bool {
			if vals[i].Count != vals[j].Count {
				return vals[i].Count > vals[j].Count
			}
			return vals[i].Value < vals[j].Value
		})
		if len(vals) > limit {
			vals = vals[:limit]
		}
		res.Values[f] = vals
	}
	return res, nil
}

func (s *Storage) Suggest(_ context.Context, dc, field, prefix string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	seen := map[string]bool{}
	for _, e := range s.matching(storage.Query{Datacenter: dc}) {
		for _, v := range storage.FieldValues(e, field) {
			if strings.HasPrefix(v, prefix) {
				seen[v] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Storage) Instance(_ context.Context, dc, node, serviceID string) (*tl.Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.instances[instanceKey{dc, node, serviceID}]
	if !ok {
		return nil, nil
	}
	return &in, nil
}

func (s *Storage) Datacenters(context.Context) ([]string, error) {
	seen := map[string]bool{}
	for _, e := range s.matching(storage.Query{}) {
		seen[e.Datacenter] = true
	}
	s.mu.RLock()
	for k := range s.instances {
		seen[k.dc] = true
	}
	s.mu.RUnlock()
	out := make([]string, 0, len(seen))
	for dc := range seen {
		out = append(out, dc)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Storage) Since(_ context.Context, dc string, after time.Time, limit int) ([]tl.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	all := s.matching(storage.Query{Datacenter: dc, From: after.Add(time.Millisecond)})
	// matching is newest first; replay wants oldest first
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
