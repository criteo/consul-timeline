// Command consul-timeline watches a Consul datacenter and records every
// health transition, serving them live and from history.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/criteo/consul-timeline/consul"
	"github.com/criteo/consul-timeline/server"
	"github.com/criteo/consul-timeline/storage"
	"github.com/criteo/consul-timeline/storage/memory"
	"github.com/criteo/consul-timeline/storage/mysql"
	tl "github.com/criteo/consul-timeline/timeline"
	"github.com/criteo/consul-timeline/watch"
)

// version is set at build time.
var version = "dev"

const (
	watchBuffer      = 10000  // events the watcher can queue before it waits
	storageQueue     = 100000 // events waiting for the storage writer
	maintainInterval = 10 * time.Minute
)

func main() {
	cfg := GetConfig()
	if cfg.Mysql.PrintSchema {
		mysql.PrintSchema()
		return
	}
	setupLogging(cfg.LogLevel, cfg.LogFormat)
	slog.Info("consul-timeline starting", "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	consulClient, err := consul.New(cfg.Consul)
	if err != nil {
		fatal(err)
	}

	var strg storage.Storage
	switch cfg.Storage {
	case mysql.Name:
		strg, err = mysql.New(cfg.Mysql)
		if err != nil {
			fatal(err)
		}
	default:
		slog.Warn("storing events in memory only", "max", cfg.Memory.MaxSize)
		strg = memory.New(cfg.Memory)
	}
	if cfg.Consul.EnableDistributedLock {
		d := storage.NewDistributed(consulClient, strg)
		defer d.Stop()
		strg = d
	}
	strg = storage.NewMetrics(strg)

	w := watch.New(consulClient, cfg.Derive, watchBuffer)
	hub := server.NewHub()
	srv := server.New(cfg.Server, strg, w, hub, version, cfg.Mysql.RetentionDays)
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Run(ctx) }()

	w.Run(ctx) // returns once the datacenter is known

	// live events go to the stream hub at once and to storage in batches
	toStorage := make(chan tl.Event, storageQueue)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-w.Events():
				hub.Publish(e)
				select {
				case toStorage <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	writerDone := make(chan struct{})
	go func() {
		storage.RunWriter(ctx, strg, toStorage, w.Instances(), storage.WriterConfig{})
		close(writerDone)
	}()
	go maintain(ctx, strg)

	if err := <-serverErr; err != nil {
		fatal(err)
	}
	slog.Info("stopping")
	select {
	case <-writerDone:
	case <-time.After(10 * time.Second):
		slog.Warn("storage writer did not drain in time")
	}
}

// maintain runs storage housekeeping periodically; the storage decides
// whether this instance should actually do it.
func maintain(ctx context.Context, m storage.Maintainer) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		mctx, cancel := context.WithTimeout(ctx, maintainInterval)
		if err := m.Maintain(mctx); err != nil {
			slog.Error("storage maintenance", "err", err)
		}
		cancel()
		t.Reset(maintainInterval)
	}
}

func setupLogging(level, format string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func fatal(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}
