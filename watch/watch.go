// Package watch turns the state of a Consul datacenter into a stream of
// timeline events: one blocking watch per service and per node, each
// diffing consecutive states.
package watch

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/criteo/consul-timeline/consul"
	tl "github.com/criteo/consul-timeline/timeline"
)

const (
	waitOnErr       = 5 * time.Second
	instanceRefresh = 24 * time.Hour // re-announce unchanged instances this often
)

type Watcher struct {
	src    Source
	derive tl.Derive
	dc     atomic.Value // string, set by Run

	events    chan tl.Event
	instances chan tl.Instance

	mu       sync.Mutex
	services map[string]chan struct{} // service name -> stop
	nodes    map[string]chan struct{} // node name -> stop
	nodeIPs  map[string]string

	ready readiness
}

// New creates a watcher; call Run to start it. Both output channels hold
// bufferSize items; the watcher blocks on full channels rather than drop.
func New(src Source, derive tl.Derive, bufferSize int) *Watcher {
	return &Watcher{
		src:       src,
		derive:    derive,
		events:    make(chan tl.Event, bufferSize),
		instances: make(chan tl.Instance, bufferSize),
		services:  make(map[string]chan struct{}),
		nodes:     make(map[string]chan struct{}),
		nodeIPs:   make(map[string]string),
		ready:     readiness{done: make(chan struct{})},
	}
}

// Run starts watching and returns immediately. Ready is closed once every
// service and node present at startup has been fetched once.
func (w *Watcher) Run(ctx context.Context) {
	dc := w.src.Datacenter()
	w.dc.Store(dc)
	slog.Info("watch: starting", "datacenter", dc)
	go w.watchServices(ctx)
	go w.watchNodes(ctx)
	go func() {
		select {
		case <-w.ready.done:
			slog.Info("watch: ready", "datacenter", dc)
		case <-ctx.Done():
		}
	}()
}

func (w *Watcher) Ready() <-chan struct{}        { return w.ready.done }
func (w *Watcher) Events() <-chan tl.Event       { return w.events }
func (w *Watcher) Instances() <-chan tl.Instance { return w.instances }

// Datacenter is the watched datacenter, "" until Run has discovered it.
func (w *Watcher) Datacenter() string {
	if v, ok := w.dc.Load().(string); ok {
		return v
	}
	return ""
}

// ServiceNames lists the services currently watched, sorted.
func (w *Watcher) ServiceNames() []string {
	w.mu.Lock()
	res := make([]string, 0, len(w.services))
	for s := range w.services {
		res = append(res, s)
	}
	w.mu.Unlock()
	sort.Strings(res)
	return res
}

// NodeNames lists the nodes currently watched, sorted.
func (w *Watcher) NodeNames() []string {
	w.mu.Lock()
	res := make([]string, 0, len(w.nodes))
	for n := range w.nodes {
		res = append(res, n)
	}
	w.mu.Unlock()
	sort.Strings(res)
	return res
}

// readiness counts the initial fetches still outstanding.
type readiness struct {
	mu      sync.Mutex
	lists   int
	pending int
	closed  bool
	done    chan struct{}
}

func (r *readiness) listArrived(children int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	r.pending += children
	r.check()
}

func (r *readiness) childReady() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending--
	r.check()
}

func (r *readiness) check() {
	if !r.closed && r.lists == 2 && r.pending <= 0 {
		r.closed = true
		close(r.done)
	}
}

// sleep waits d unless ctx or stop end first; it reports whether to go on.
func sleep(ctx context.Context, stop <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-t.C:
		return true
	}
}

func nextIndex(prev, got uint64) uint64 {
	if got < prev { // index reset on the servers: start over
		return 0
	}
	return got
}

// ---- service list and per-service watches ---------------------------------

func (w *Watcher) watchServices(ctx context.Context) {
	var idx uint64
	first := true
	for ctx.Err() == nil {
		res, err := w.src.Services(idx)
		if err != nil {
			slog.Error("watch: service list", "err", err)
			if !sleep(ctx, nil, waitOnErr) {
				return
			}
			continue
		}
		if first || res.Index != idx {
			w.syncServices(ctx, res.Services, first)
		}
		first = false
		idx = nextIndex(idx, res.Index)
	}
}

func (w *Watcher) syncServices(ctx context.Context, services map[string][]string, initial bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	added := 0
	for name := range services {
		if _, ok := w.services[name]; ok {
			continue
		}
		stop := make(chan struct{})
		w.services[name] = stop
		added++
		go w.watchService(ctx, name, stop, initial)
	}
	for name, stop := range w.services {
		if _, ok := services[name]; !ok {
			close(stop)
			delete(w.services, name)
		}
	}
	if initial {
		w.ready.listArrived(added)
	}
	watchesGauge.WithLabelValues("service").Set(float64(len(w.services)))
}

func (w *Watcher) watchService(ctx context.Context, name string, stop <-chan struct{}, countReady bool) {
	var idx uint64
	var last []consul.CheckServiceNode
	marks := map[instanceKey]instanceMark{}
	first := true
	for {
		select {
		case <-stop:
			// the service left the catalog: whatever we still knew is gone
			if len(last) > 0 {
				w.compareService(time.Now(), idx, last, nil, marks)
			}
			return
		case <-ctx.Done():
			return
		default:
		}

		res, err := w.src.Service(idx, name)
		if err != nil {
			slog.Warn("watch: service", "service", name, "err", err)
			if !sleep(ctx, stop, waitOnErr) {
				return
			}
			continue
		}
		at := time.Now()

		if first {
			first = false
			if countReady {
				w.ready.childReady()
			}
			for _, csn := range res.Nodes {
				w.announce(at, csn, marks, true)
			}
		} else if res.Index != idx {
			w.compareService(at, res.Index, last, res.Nodes, marks)
		} else {
			for _, csn := range res.Nodes { // wait timed out: refresh stale announcements
				w.announce(at, csn, marks, false)
			}
		}
		last = res.Nodes
		idx = nextIndex(idx, res.Index)
	}
}

// ---- node list and per-node watches ---------------------------------------

func (w *Watcher) watchNodes(ctx context.Context) {
	var idx uint64
	first := true
	for ctx.Err() == nil {
		res, err := w.src.Nodes(idx)
		if err != nil {
			slog.Error("watch: node list", "err", err)
			if !sleep(ctx, nil, waitOnErr) {
				return
			}
			continue
		}
		if first || res.Index != idx {
			w.syncNodes(ctx, res.Nodes, res.Index, first)
		}
		first = false
		idx = nextIndex(idx, res.Index)
	}
}

func (w *Watcher) syncNodes(ctx context.Context, nodes []*consul.Node, index uint64, initial bool) {
	at := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	present := make(map[string]bool, len(nodes))
	added := 0
	for _, n := range nodes {
		present[n.Node] = true
		w.nodeIPs[n.Node] = n.Address
		if _, ok := w.nodes[n.Node]; ok {
			continue
		}
		stop := make(chan struct{})
		w.nodes[n.Node] = stop
		added++
		if !initial {
			w.emit(tl.Event{
				Time: at, Datacenter: w.Datacenter(), Kind: tl.KindNode, ConsulIndex: index,
				NodeName: n.Node, NodeIP: n.Address,
				OldNodeStatus: tl.StatusMissing, NewNodeStatus: tl.StatusUnknown,
			})
		}
		go w.watchNode(ctx, n, stop, initial)
	}
	for name, stop := range w.nodes {
		if !present[name] {
			close(stop)
			delete(w.nodes, name)
			delete(w.nodeIPs, name)
		}
	}
	if initial {
		w.ready.listArrived(added)
	}
	watchesGauge.WithLabelValues("node").Set(float64(len(w.nodes)))
}

func (w *Watcher) watchNode(ctx context.Context, node *consul.Node, stop <-chan struct{}, countReady bool) {
	var idx uint64
	var last []*consul.HealthCheck
	first := true
	for {
		select {
		case <-stop:
			// the node left the catalog
			w.compareNode(time.Now(), idx, node, last, nil)
			return
		case <-ctx.Done():
			return
		default:
		}

		res, err := w.src.Node(idx, node.Node)
		if err != nil {
			slog.Warn("watch: node", "node", node.Node, "err", err)
			if !sleep(ctx, stop, waitOnErr) {
				return
			}
			continue
		}
		at := time.Now()

		checks := nodeChecks(res.HealthChecks)
		if first {
			first = false
			if countReady {
				w.ready.childReady()
			}
		} else if res.Index != idx {
			w.compareNode(at, res.Index, node, last, checks)
		}
		last = checks
		idx = nextIndex(idx, res.Index)
	}
}

// nodeChecks keeps the node-level checks; service checks belong to the
// service watches.
func nodeChecks(checks []*consul.HealthCheck) []*consul.HealthCheck {
	out := make([]*consul.HealthCheck, 0, len(checks))
	for _, c := range checks {
		if c.ServiceID == "" {
			out = append(out, c)
		}
	}
	return out
}

func (w *Watcher) emit(e tl.Event) {
	eventsCounter.WithLabelValues(e.Kind.String()).Inc()
	w.events <- e
}
