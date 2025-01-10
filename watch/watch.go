package watch

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/consul/agent/structs"
	tl "github.com/shimmerglass/consul-timeline/timeline"
	log "github.com/sirupsen/logrus"
)

const (
	waitOnErr = 5 * time.Second
)

type Watcher struct {
	c consul

	lock sync.Mutex

	services map[string]*uint32
	nodes    map[string]*uint32

	ready   bool
	readyWg sync.WaitGroup

	evtID int32
	out   chan tl.Event
}

func New(c consul, bufferSize int) *Watcher {
	return &Watcher{
		c:        c,
		services: make(map[string]*uint32),
		nodes:    make(map[string]*uint32),
		out:      make(chan tl.Event, bufferSize),
	}
}

func (w *Watcher) Run() <-chan tl.Event {
	log.Info("watch: starting")
	w.readyWg.Add(2)
	go w.watchServices()
	go w.watchNodes()
	w.readyWg.Wait()
	w.ready = true
	log.Info("watch: ready")
	return w.out
}

func (w *Watcher) FilterEntries() []string {
	res := []string{}

	w.lock.Lock()
	for s := range w.services {
		res = append(res, s)
	}
	for n := range w.nodes {
		res = append(res, n)
	}
	w.lock.Unlock()

	sort.Strings(res)
	return res
}

func (w *Watcher) sendEvent(evt tl.Event) {
	eventsCounter.Inc()
	w.out <- evt
}

func (w *Watcher) watchServices() {
	var idx uint64

	for {
		if idx > 0 {
			w.readyWg.Wait()
		}
		res, err := w.c.Services(idx)
		if err != nil {
			log.Errorf("error getting service list: %s", err)
			time.Sleep(waitOnErr)
			continue
		}

		// init
		if idx == 0 {
			w.readyWg.Add(len(res.Services))
			w.readyWg.Done()
		}

		w.handleServicesChanged(res.Services)
		idx = res.Index
	}
}

func (w *Watcher) handleServicesChanged(services map[string][]string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	for s := range services {
		if _, ok := w.services[s]; ok {
			atomic.StoreUint32(w.services[s], 0)
			continue
		}

		w.watchService(s)
	}

	for s := range w.services {
		if _, ok := services[s]; !ok {
			atomic.StoreUint32(w.services[s], 1)
		}
	}
}

func (w *Watcher) watchService(name string) {
	log.Debugf("watching service %s", name)

	stop := uint32(0)
	w.services[name] = &stop

	var lastState structs.CheckServiceNodes
	go func() {
		var idx uint64

		for {
			if atomic.LoadUint32(&stop) == 1 {
				w.lock.Lock()
				delete(w.services, name)
				w.lock.Unlock()
				log.Debugf("stopped watching service %s", name)
				return
			}

			res, err := w.c.Service(idx, name)

			if err != nil {
				log.Errorf("error getting service %s: %s", name, err)
				time.Sleep(waitOnErr)
				continue
			}

			if idx == 0 && !w.ready {
				log.Debugf("service %s ready", name)
				w.readyWg.Done()
			}

			w.lock.Lock()
			if idx > 0 && w.ready {
				w.compareServiceStates(time.Now().UTC(), lastState, res.Nodes)
			}

			lastState = res.Nodes
			w.lock.Unlock()

			idx = res.Index
		}
	}()
}

func (w *Watcher) watchNodes() {
	var idx uint64

	for {
		if idx > 0 {
			w.readyWg.Wait()
		}
		res, err := w.c.Nodes(idx)
		if err != nil {
			log.Errorf("error getting node list: %s", err)
			time.Sleep(waitOnErr)
			continue
		}

		if idx == 0 && !w.ready {
			w.readyWg.Add(len(res.Nodes))
			w.readyWg.Done()
		}

		idx = res.Index

		w.handleNodesChanged(res.Nodes)
	}
}

func (w *Watcher) handleNodesChanged(nodes []*structs.Node) {
	w.lock.Lock()
	defer w.lock.Unlock()

	new := map[string]*structs.Node{}
	for _, n := range nodes {
		new[n.Node] = n
	}

	for n, node := range new {
		if _, ok := w.nodes[n]; ok {
			atomic.StoreUint32(w.nodes[n], 0)
			continue
		}

		w.watchNode(node)
	}

	for n := range w.nodes {
		if _, ok := new[n]; !ok {
			atomic.StoreUint32(w.nodes[n], 1)
		}
	}
}

func (w *Watcher) watchNode(node *structs.Node) {
	log.Debugf("watching node %s", node.Node)

	stop := uint32(0)
	w.nodes[node.Node] = &stop

	var lastState structs.HealthChecks

	go func() {
		var idx uint64

		for {
			if idx > 0 {
				w.readyWg.Wait()
			}

			if atomic.LoadUint32(&stop) == 1 {
				w.lock.Lock()
				delete(w.nodes, node.Node)
				w.lock.Unlock()
				log.Debugf("stopped watching node %s", node.Node)
				return
			}

			res, err := w.c.Node(idx, node.Node)

			if err != nil {
				log.Errorf("error getting node %s: %s", node.Node, err)
				time.Sleep(waitOnErr)
				continue
			}

			filteredChecks := res.HealthChecks[:0]
			for _, c := range res.HealthChecks {
				if c.ServiceID == "" {
					filteredChecks = append(filteredChecks, c)
				}
			}

			if idx == 0 && !w.ready {
				log.Debugf("node %s ready", node.Node)
				w.readyWg.Done()
			}

			w.lock.Lock()
			if idx > 0 && w.ready {
				w.handleNodeChanged(node, time.Now().UTC(), lastState, filteredChecks)
			}
			lastState = filteredChecks
			w.lock.Unlock()

			idx = res.Index
		}
	}()
}

func (w *Watcher) handleNodeChanged(node *structs.Node, at time.Time, old, new structs.HealthChecks) {
	oldStatus, newStatus := tl.StatusMissing, tl.StatusMissing

	if len(old) > 0 {
		oldStatus = aggregatedStatus(old)
	}
	if len(new) > 0 {
		newStatus = aggregatedStatus(new)
	}

	base := tl.Event{
		Time:          at,
		Datacenter:    w.c.Datacenter(),
		NodeName:      node.Node,
		NodeIP:        node.Address,
		OldNodeStatus: oldStatus,
		NewNodeStatus: newStatus,
	}

	w.compareChecks(base, old, new)
}

func (w *Watcher) nextEventID() int32 {
	return atomic.AddInt32(&w.evtID, 1)
}
