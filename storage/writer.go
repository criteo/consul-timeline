package storage

import (
	"context"
	"log/slog"
	"time"

	tl "github.com/criteo/consul-timeline/timeline"
)

// WriterConfig tunes RunWriter.
type WriterConfig struct {
	BatchSize     int
	FlushInterval time.Duration
}

// RunWriter drains the watcher's channels into w in batches, so that the
// watcher never waits on the database and bursts become few large inserts.
// A batch that still fails after a few retries is dropped and counted.
func RunWriter(ctx context.Context, w Writer, events <-chan tl.Event, instances <-chan tl.Instance, cfg WriterConfig) {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 200 * time.Millisecond
	}

	evBatch := make([]tl.Event, 0, cfg.BatchSize)
	instBatch := make([]tl.Instance, 0, cfg.BatchSize)
	ticker := time.NewTicker(cfg.FlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(evBatch) > 0 {
			batch := evBatch
			persist(ctx, "events", len(batch), func(ctx context.Context) error { return w.StoreEvents(ctx, batch) })
			evBatch = make([]tl.Event, 0, cfg.BatchSize)
		}
		if len(instBatch) > 0 {
			batch := instBatch
			persist(ctx, "instances", len(batch), func(ctx context.Context) error { return w.UpsertInstances(ctx, batch) })
			instBatch = make([]tl.Instance, 0, cfg.BatchSize)
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case e := <-events:
			evBatch = append(evBatch, e)
			if len(evBatch) >= cfg.BatchSize {
				flush()
			}
		case i := <-instances:
			instBatch = append(instBatch, i)
			if len(instBatch) >= cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func persist(ctx context.Context, what string, n int, fn func(context.Context) error) {
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := fn(callCtx)
		cancel()
		if err == nil {
			return
		}
		if attempt >= 3 || ctx.Err() != nil {
			slog.Error("storage: dropping batch", "what", what, "rows", n, "err", err)
			droppedCounter.WithLabelValues(what).Add(float64(n))
			return
		}
		slog.Warn("storage: write failed, retrying", "what", what, "rows", n, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}
