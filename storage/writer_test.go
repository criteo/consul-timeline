package storage

import (
	"context"
	"testing"
	"time"

	tl "github.com/criteo/consul-timeline/timeline"
)

type recordingWriter struct {
	events []tl.Event
	ctxErr error
}

func (w *recordingWriter) StoreEvents(ctx context.Context, events []tl.Event) error {
	w.events = append(w.events, events...)
	w.ctxErr = ctx.Err()
	return nil
}

func (w *recordingWriter) UpsertInstances(context.Context, []tl.Instance) error { return nil }

// A shutdown must flush what is queued with a live context, not the
// cancelled one that triggered it.
func TestRunWriterFlushesOnShutdown(t *testing.T) {
	w := &recordingWriter{}
	events := make(chan tl.Event, 1)
	events <- tl.Event{ServiceName: "web"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		RunWriter(ctx, w, events, nil, WriterConfig{FlushInterval: time.Hour})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not stop")
	}
	if len(w.events) != 1 {
		t.Fatalf("stored %d events, want 1", len(w.events))
	}
	if w.ctxErr != nil {
		t.Fatalf("flush ran with a cancelled context: %v", w.ctxErr)
	}
}
