package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/criteo/consul-timeline/consul"
	"github.com/criteo/consul-timeline/storage"

	_ "github.com/go-sql-driver/mysql"

	"github.com/criteo/consul-timeline/server"
	"github.com/criteo/consul-timeline/storage/memory"
	"github.com/criteo/consul-timeline/storage/mysql"
	tl "github.com/criteo/consul-timeline/timeline"
	"github.com/criteo/consul-timeline/watch"
	log "github.com/sirupsen/logrus"
)

const (
	eventsBuffer = 200
)

func dupEvents(in <-chan tl.Event) (<-chan tl.Event, <-chan tl.Event) {
	o1 := make(chan tl.Event, eventsBuffer)
	o2 := make(chan tl.Event, eventsBuffer)
	go func() {
		for e := range in {
			o1 <- e
			o2 <- e
		}
	}()

	return o1, o2
}

func main() {
	cfg := GetConfig()

	if cfg.Mysql.PrintSchema {
		mysql.PrintSchema()
		return
	}

	logLvl, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.Fatal(err)
	}

	log.SetLevel(logLvl)

	// consul client
	consul := consul.New(cfg.Consul)

	// storage
	var strg storage.Storage

	switch cfg.Storage {
	case mysql.Name:
		strg, err = mysql.New(cfg.Mysql, consul.Datacenter)
		if err != nil {
			log.Fatal(err)
		}
	case memory.Name:
		fallthrough
	default:
		log.Warnf("storing up to %d events in memory", cfg.Memory.MaxSize)
		strg = memory.New(cfg.Memory)
	}

	if cfg.Consul.EnableDistributedLock {
		dstrg := storage.NewDistributed(consul, strg)
		defer dstrg.Stop()
		strg = dstrg
	}

	strg = storage.NewMetrics(strg)

	// consul watch
	w := watch.New(consul, eventsBuffer)
	events := w.Run()

	storageEvents, apiEvents := dupEvents(events)

	// HTTP api
	api := server.New(cfg.Server, strg, w, apiEvents)
	go func() {
		err := api.Serve()
		if err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		for e := range storageEvents {
			err := strg.Store(e)
			if err != nil {
				log.Error(err)
			}
		}
	}()

	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	<-c
	log.Info("stopping...")
}
