package storage

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/criteo/consul-timeline/consul"
	tl "github.com/criteo/consul-timeline/timeline"
)

var _ Storage = (*Distributed)(nil)

// Distributed lets several instances share one database: only the holder
// of a Consul lock writes and runs maintenance, everyone reads.
type Distributed struct {
	consul *consul.Consul
	inner  Storage

	stop   chan struct{}
	done   sync.WaitGroup
	leader atomic.Bool
}

func NewDistributed(c *consul.Consul, inner Storage) *Distributed {
	s := &Distributed{
		consul: c,
		inner:  inner,
		stop:   make(chan struct{}),
	}
	go s.lockLoop()
	return s
}

func (s *Distributed) lockLoop() {
	var lock *api.Lock
	for {
		l, err := s.consul.Lock()
		if err != nil {
			slog.Error("storage lock", "err", err)
			time.Sleep(time.Second)
			continue
		}
		lock = l
		break
	}

	for {
		isLeaderGauge.Set(0)
		slog.Info("storage: acquiring lock")
		lockChan, err := lock.Lock(nil)
		if err != nil {
			slog.Error("storage lock", "err", err)
			time.Sleep(time.Second)
			continue
		}

		slog.Info("storage: lock acquired, this instance writes")
		isLeaderGauge.Set(1)
		s.done.Add(1)
		s.leader.Store(true)
		select {
		case <-lockChan:
			s.leader.Store(false)
			slog.Info("storage: lock lost")
			s.done.Done()
		case <-s.stop:
			s.leader.Store(false)
			slog.Info("storage: releasing lock")
			if err := lock.Unlock(); err != nil {
				slog.Error("storage unlock", "err", err)
			}
			s.done.Done()
			return
		}
	}
}

func (s *Distributed) Stop() {
	close(s.stop)
	s.done.Wait()
}

func (s *Distributed) IsLeader() bool { return s.leader.Load() }

func (s *Distributed) StoreEvents(ctx context.Context, events []tl.Event) error {
	if !s.leader.Load() {
		return nil
	}
	return s.inner.StoreEvents(ctx, events)
}

func (s *Distributed) UpsertInstances(ctx context.Context, instances []tl.Instance) error {
	if !s.leader.Load() {
		return nil
	}
	return s.inner.UpsertInstances(ctx, instances)
}

func (s *Distributed) Maintain(ctx context.Context) error {
	if !s.leader.Load() {
		return nil
	}
	return s.inner.Maintain(ctx)
}

func (s *Distributed) Events(ctx context.Context, q Query) (Page, error) {
	return s.inner.Events(ctx, q)
}

func (s *Distributed) Histogram(ctx context.Context, q Query, buckets int) ([]Bucket, bool, error) {
	return s.inner.Histogram(ctx, q, buckets)
}

func (s *Distributed) Facets(ctx context.Context, q Query, fields []string, limit int) (Facets, error) {
	return s.inner.Facets(ctx, q, fields, limit)
}

func (s *Distributed) Suggest(ctx context.Context, dc, field, prefix string, limit int) ([]string, error) {
	return s.inner.Suggest(ctx, dc, field, prefix, limit)
}

func (s *Distributed) Instance(ctx context.Context, dc, node, serviceID string) (*tl.Instance, error) {
	return s.inner.Instance(ctx, dc, node, serviceID)
}

func (s *Distributed) Datacenters(ctx context.Context) ([]string, error) {
	return s.inner.Datacenters(ctx)
}

func (s *Distributed) Since(ctx context.Context, dc string, after time.Time, limit int) ([]tl.Event, error) {
	return s.inner.Since(ctx, dc, after, limit)
}
