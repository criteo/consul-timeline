package watch

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	eventsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "consul_timeline_events_total",
		Help: "Events emitted by the watcher, by kind",
	}, []string{"kind"})

	watchesGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "consul_timeline_watches",
		Help: "Blocking watches currently open, by type",
	}, []string{"type"})

	instanceUpserts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "consul_timeline_instance_upserts_total",
		Help: "Instance records announced to storage",
	})
)
