package watch

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/criteo/consul-timeline/consul"
	tl "github.com/criteo/consul-timeline/timeline"
)

type instanceKey struct {
	node, id string
}

// instanceMark remembers what was last announced about an instance so that
// unchanged registrations are not re-sent more than once a day.
type instanceMark struct {
	fingerprint uint64
	announced   time.Time
}

// compareService diffs two states of one service and emits the events in
// between: instance registrations and deregistrations, and status changes
// of service-level checks. Node-level checks are reported by the node
// watches, not once per instance.
func (w *Watcher) compareService(at time.Time, index uint64, old, new []consul.CheckServiceNode, marks map[instanceKey]instanceMark) {
	oldIdx, oldHealthy := indexInstances(old)
	newIdx, newHealthy := indexInstances(new)

	base := func(csn consul.CheckServiceNode) tl.Event {
		team, app, version := w.derive.Apply(csn.Service.Meta)
		return tl.Event{
			Time:           at,
			Datacenter:     w.Datacenter(),
			ConsulIndex:    index,
			NodeName:       csn.Node.Node,
			NodeIP:         csn.Node.Address,
			ServiceName:    csn.Service.Service,
			ServiceID:      csn.Service.ID,
			Team:           team,
			App:            app,
			Version:        version,
			Tags:           append([]string(nil), csn.Service.Tags...),
			OldHealthy:     oldHealthy,
			NewHealthy:     newHealthy,
			TotalInstances: len(new),
		}
	}

	for key, ns := range newIdx {
		os, existed := oldIdx[key]
		if !existed {
			e := base(ns)
			e.Kind = tl.KindInstance
			e.OldServiceStatus = tl.StatusMissing
			e.NewServiceStatus = instanceStatus(ns.Checks)
			w.emit(e)
			w.announce(at, ns, marks, true)
			continue
		}
		w.announce(at, ns, marks, false)
		w.compareChecks(base(ns), tl.KindCheck, serviceChecks(os.Checks), serviceChecks(ns.Checks), func(e *tl.Event) {
			e.OldServiceStatus = instanceStatus(os.Checks)
			e.NewServiceStatus = instanceStatus(ns.Checks)
		})
	}

	for key, os := range oldIdx {
		if _, ok := newIdx[key]; ok {
			continue
		}
		e := base(os)
		e.Kind = tl.KindInstance
		e.OldServiceStatus = instanceStatus(os.Checks)
		e.NewServiceStatus = tl.StatusMissing
		w.emit(e)
		delete(marks, key)
	}
}

// compareNode diffs the node-level checks of one node. A nil new state
// means the node left the catalog.
func (w *Watcher) compareNode(at time.Time, index uint64, node *consul.Node, old, new []*consul.HealthCheck) {
	base := tl.Event{
		Time:          at,
		Datacenter:    w.Datacenter(),
		ConsulIndex:   index,
		Kind:          tl.KindNode,
		NodeName:      node.Node,
		NodeIP:        node.Address,
		OldNodeStatus: nodeStatus(old),
		NewNodeStatus: nodeStatus(new),
	}
	if new == nil {
		w.emit(base) // deregistered; per-check detail would only repeat it
		return
	}
	w.compareChecks(base, tl.KindNode, old, new, nil)
}

// compareChecks emits one event per check whose status differs between old
// and new, including checks that appeared or disappeared.
func (w *Watcher) compareChecks(base tl.Event, kind tl.Kind, old, new []*consul.HealthCheck, decorate func(*tl.Event)) {
	oldIdx := make(map[string]*consul.HealthCheck, len(old))
	for _, c := range old {
		oldIdx[c.CheckID] = c
	}
	newIdx := make(map[string]*consul.HealthCheck, len(new))
	for _, c := range new {
		newIdx[c.CheckID] = c
	}

	emit := func(c *consul.HealthCheck, oldStatus, newStatus tl.Status) {
		e := base
		e.Kind = kind
		e.CheckID = c.CheckID
		e.CheckName = c.Name
		e.CheckType = c.Type
		e.CheckOutput = c.Output
		e.OldCheckStatus = oldStatus
		e.NewCheckStatus = newStatus
		if decorate != nil {
			decorate(&e)
		}
		w.emit(e)
	}

	for id, nc := range newIdx {
		oc, ok := oldIdx[id]
		switch {
		case !ok:
			emit(nc, tl.StatusMissing, tl.StatusFromConsul(nc.Status))
		case oc.Status != nc.Status:
			emit(nc, tl.StatusFromConsul(oc.Status), tl.StatusFromConsul(nc.Status))
		}
	}
	for id, oc := range oldIdx {
		if _, ok := newIdx[id]; !ok {
			gone := *oc
			gone.Output = ""
			emit(&gone, tl.StatusFromConsul(oc.Status), tl.StatusMissing)
		}
	}
}

// announce sends the instance record to storage when it is new, when its
// registration changed, or when the last announcement is older than
// instanceRefresh.
func (w *Watcher) announce(at time.Time, csn consul.CheckServiceNode, marks map[instanceKey]instanceMark, force bool) {
	key := instanceKey{csn.Node.Node, csn.Service.ID}
	fp := fingerprint(csn)
	m, known := marks[key]
	if !force && known && m.fingerprint == fp && at.Sub(m.announced) < instanceRefresh {
		return
	}
	marks[key] = instanceMark{fingerprint: fp, announced: at}

	team, app, version := w.derive.Apply(csn.Service.Meta)
	instanceUpserts.Inc()
	w.instances <- tl.Instance{
		Datacenter:  w.Datacenter(),
		NodeName:    csn.Node.Node,
		ServiceID:   csn.Service.ID,
		ServiceName: csn.Service.Service,
		NodeIP:      csn.Node.Address,
		Address:     csn.Service.Address,
		Port:        csn.Service.Port,
		Team:        team,
		App:         app,
		Version:     version,
		Tags:        append([]string(nil), csn.Service.Tags...),
		Meta:        copyMap(csn.Service.Meta),
		NodeMeta:    copyMap(csn.Node.Meta),
		FirstSeen:   at,
		LastSeen:    at,
	}
}

func fingerprint(csn consul.CheckServiceNode) uint64 {
	h := fnv.New64a()
	w := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	w(csn.Service.Service)
	w(csn.Service.Address)
	w(strconv.Itoa(csn.Service.Port))
	tags := append([]string(nil), csn.Service.Tags...)
	sort.Strings(tags)
	for _, t := range tags {
		w(t)
	}
	writeMap(w, csn.Service.Meta)
	writeMap(w, csn.Node.Meta)
	return h.Sum64()
}

func writeMap(w func(string), m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		w(k + "=" + m[k])
	}
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// indexInstances keys instances by (node, service id) and counts the
// healthy ones, health being the aggregate of all their checks including
// the node's, as Consul itself filters.
func indexInstances(in []consul.CheckServiceNode) (map[instanceKey]consul.CheckServiceNode, int) {
	idx := make(map[instanceKey]consul.CheckServiceNode, len(in))
	healthy := 0
	for _, csn := range in {
		if csn.Node == nil || csn.Service == nil {
			continue
		}
		if instanceStatus(csn.Checks) == tl.StatusPassing {
			healthy++
		}
		idx[instanceKey{csn.Node.Node, csn.Service.ID}] = csn
	}
	return idx, healthy
}

func serviceChecks(checks []*consul.HealthCheck) []*consul.HealthCheck {
	out := make([]*consul.HealthCheck, 0, len(checks))
	for _, c := range checks {
		if c.ServiceID != "" {
			out = append(out, c)
		}
	}
	return out
}

// instanceStatus aggregates the checks of an instance: maintenance wins,
// then critical, warning, passing. No checks at all means passing.
func instanceStatus(checks []*consul.HealthCheck) tl.Status {
	if len(checks) == 0 {
		return tl.StatusPassing
	}
	return aggregate(checks)
}

// nodeStatus aggregates node-level checks; no checks means the node is gone.
func nodeStatus(checks []*consul.HealthCheck) tl.Status {
	if len(checks) == 0 {
		return tl.StatusMissing
	}
	return aggregate(checks)
}

func aggregate(checks []*consul.HealthCheck) tl.Status {
	var passing, warning, critical, maintenance bool
	for _, c := range checks {
		if c.CheckID == api.NodeMaint || strings.HasPrefix(c.CheckID, api.ServiceMaintPrefix) {
			maintenance = true
			continue
		}
		switch c.Status {
		case api.HealthPassing:
			passing = true
		case api.HealthWarning:
			warning = true
		case api.HealthCritical:
			critical = true
		}
	}
	switch {
	case maintenance:
		return tl.StatusMaintenance
	case critical:
		return tl.StatusCritical
	case warning:
		return tl.StatusWarning
	case passing:
		return tl.StatusPassing
	default:
		return tl.StatusUnknown
	}
}
