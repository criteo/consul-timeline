package storage

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	tl "github.com/criteo/consul-timeline/timeline"
)

var (
	isLeaderGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "consul_timeline_storage_is_leader",
		Help: "1 when this instance holds the storage lock and writes",
	})

	opHistogram = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "consul_timeline_storage_seconds",
		Help:    "Storage operation latency",
		Buckets: prometheus.ExponentialBuckets(0.0005, 4, 9),
	}, []string{"op"})

	// The two histograms below predate the op label; recording rules and
	// dashboards read them, so they stay.
	writeHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "consul_timeline_storage_write_seconds",
		Help:    "Event batch write latency",
		Buckets: prometheus.ExponentialBuckets(0.0001, 4, 10),
	})
	readHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "consul_timeline_storage_read_seconds",
		Help:    "Event query latency",
		Buckets: prometheus.ExponentialBuckets(0.0001, 4, 10),
	})

	storedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "consul_timeline_storage_events_stored_total",
		Help: "Events written to storage",
	})

	droppedCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "consul_timeline_storage_dropped_total",
		Help: "Rows dropped after repeated write failures",
	}, []string{"what"})
)

var _ Storage = (*Metrics)(nil)

// Metrics wraps a Storage with latency and volume metrics.
type Metrics struct {
	inner Storage
}

func NewMetrics(inner Storage) *Metrics {
	return &Metrics{inner}
}

func observe(op string, start time.Time) {
	opHistogram.WithLabelValues(op).Observe(time.Since(start).Seconds())
}

func (s *Metrics) StoreEvents(ctx context.Context, events []tl.Event) error {
	start := time.Now()
	defer observe("store_events", start)
	defer func() { writeHistogram.Observe(time.Since(start).Seconds()) }()
	err := s.inner.StoreEvents(ctx, events)
	if err == nil {
		storedCounter.Add(float64(len(events)))
	}
	return err
}

func (s *Metrics) UpsertInstances(ctx context.Context, instances []tl.Instance) error {
	defer observe("upsert_instances", time.Now())
	return s.inner.UpsertInstances(ctx, instances)
}

func (s *Metrics) Maintain(ctx context.Context) error {
	defer observe("maintain", time.Now())
	return s.inner.Maintain(ctx)
}

func (s *Metrics) Events(ctx context.Context, q Query) (Page, error) {
	start := time.Now()
	defer observe("events", start)
	defer func() { readHistogram.Observe(time.Since(start).Seconds()) }()
	return s.inner.Events(ctx, q)
}

func (s *Metrics) Histogram(ctx context.Context, q Query, buckets int) ([]Bucket, bool, error) {
	defer observe("histogram", time.Now())
	return s.inner.Histogram(ctx, q, buckets)
}

func (s *Metrics) Facets(ctx context.Context, q Query, fields []string, limit int) (Facets, error) {
	defer observe("facets", time.Now())
	return s.inner.Facets(ctx, q, fields, limit)
}

func (s *Metrics) Suggest(ctx context.Context, dc, field, prefix string, limit int) ([]string, error) {
	defer observe("suggest", time.Now())
	return s.inner.Suggest(ctx, dc, field, prefix, limit)
}

func (s *Metrics) Instance(ctx context.Context, dc, node, serviceID string) (*tl.Instance, error) {
	defer observe("instance", time.Now())
	return s.inner.Instance(ctx, dc, node, serviceID)
}

func (s *Metrics) Datacenters(ctx context.Context) ([]string, error) {
	defer observe("datacenters", time.Now())
	return s.inner.Datacenters(ctx)
}

func (s *Metrics) Since(ctx context.Context, dc string, after time.Time, limit int) ([]tl.Event, error) {
	defer observe("since", time.Now())
	return s.inner.Since(ctx, dc, after, limit)
}
