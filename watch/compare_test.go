package watch

import (
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/require"

	"github.com/criteo/consul-timeline/consul"
	tl "github.com/criteo/consul-timeline/timeline"
)

func newTestWatcher() *Watcher {
	w := New(nil, tl.DefaultDerive, 100)
	w.dc.Store("test")
	return w
}

func (w *Watcher) drain() (events []tl.Event, instances []tl.Instance) {
	for {
		select {
		case e := <-w.events:
			events = append(events, e)
		case i := <-w.instances:
			instances = append(instances, i)
		default:
			return
		}
	}
}

func check(id, name, status, serviceID string) *consul.HealthCheck {
	return &consul.HealthCheck{CheckID: id, Name: name, Status: status, ServiceID: serviceID, Type: "http", Output: "out " + status}
}

func serf(status string) *consul.HealthCheck {
	return &consul.HealthCheck{CheckID: "serfHealth", Name: "Serf Health Status", Status: status, Type: "serf"}
}

func instance(node, id string, port int, checks ...*consul.HealthCheck) consul.CheckServiceNode {
	return consul.CheckServiceNode{
		Node:    &consul.Node{Node: node, Address: "10.0.0." + node[len(node)-1:], Meta: map[string]string{"rack_name": "07.04"}},
		Service: &consul.NodeService{ID: id, Service: "web-frontend", Port: port, Address: "10.48.1.2", Tags: []string{"http"}, Meta: map[string]string{"team": "payments", "version": "1"}},
		Checks:  checks,
	}
}

func byKind(events []tl.Event, k tl.Kind) []tl.Event {
	var out []tl.Event
	for _, e := range events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func TestCompareServiceRegistration(t *testing.T) {
	w := newTestWatcher()
	marks := map[instanceKey]instanceMark{}
	at := time.Now()

	old := []consul.CheckServiceNode{instance("n1", "kubernetes-pod-web-frontend-10.48.1.2-8080", 8080, serf(api.HealthPassing), check("c1", "kubernetes_http_check_0", api.HealthPassing, "kubernetes-pod-web-frontend-10.48.1.2-8080"))}
	new := append(old, instance("n2", "kubernetes-pod-web-frontend-10.48.1.3-8080", 8080, serf(api.HealthPassing), check("c2", "kubernetes_http_check_0", api.HealthCritical, "kubernetes-pod-web-frontend-10.48.1.3-8080")))
	marks[instanceKey{"n1", old[0].Service.ID}] = instanceMark{fingerprint: fingerprint(old[0]), announced: at}

	w.compareService(at, 42, old, new, marks)
	events, instances := w.drain()

	require.Len(t, events, 1, "a new instance is one event, not one per check")
	e := events[0]
	require.Equal(t, tl.KindInstance, e.Kind)
	require.Equal(t, "n2", e.NodeName)
	require.Equal(t, tl.StatusMissing, e.OldServiceStatus)
	require.Equal(t, tl.StatusCritical, e.NewServiceStatus, "aggregated over the instance's checks")
	require.Equal(t, 1, e.OldHealthy)
	require.Equal(t, 1, e.NewHealthy)
	require.Equal(t, 2, e.TotalInstances)
	require.Equal(t, []string{"http"}, e.Tags, "the instance's tags ride on the event")
	require.Equal(t, "payments", e.Team)
	require.Equal(t, "1", e.Version)
	require.EqualValues(t, 42, e.ConsulIndex)
	require.Equal(t, "test", e.Datacenter)

	require.Len(t, instances, 1, "the new instance is announced")
	require.Equal(t, "n2", instances[0].NodeName)
	require.Equal(t, map[string]string{"rack_name": "07.04"}, instances[0].NodeMeta)
	require.Equal(t, []string{"http"}, instances[0].Tags)
}

func TestCompareServiceCheckChange(t *testing.T) {
	w := newTestWatcher()
	marks := map[instanceKey]instanceMark{}
	id := "kubernetes-pod-web-frontend-10.48.1.2-8080"

	old := []consul.CheckServiceNode{instance("n1", id, 8080, serf(api.HealthPassing), check("c1", "kubernetes_http_check_0", api.HealthPassing, id), check("c2", "kubernetes_http_check_1", api.HealthPassing, id))}
	new := []consul.CheckServiceNode{instance("n1", id, 8080, serf(api.HealthCritical), check("c1", "kubernetes_http_check_0", api.HealthCritical, id), check("c2", "kubernetes_http_check_1", api.HealthPassing, id))}

	w.compareService(time.Now(), 7, old, new, marks)
	events, instances := w.drain()

	require.Len(t, events, 1, "only the service check that changed; the serf check belongs to the node watch")
	e := events[0]
	require.Equal(t, tl.KindCheck, e.Kind)
	require.Equal(t, "c1", e.CheckID)
	require.Equal(t, "kubernetes_http_check_0", e.CheckName)
	require.Equal(t, "http", e.CheckType)
	require.Equal(t, tl.StatusPassing, e.OldCheckStatus)
	require.Equal(t, tl.StatusCritical, e.NewCheckStatus)
	require.Equal(t, "out critical", e.CheckOutput)
	require.Equal(t, tl.StatusPassing, e.OldServiceStatus)
	require.Equal(t, tl.StatusCritical, e.NewServiceStatus)
	require.Equal(t, 1, e.OldHealthy)
	require.Equal(t, 0, e.NewHealthy, "the instance is no longer healthy")
	require.Equal(t, tl.StatusCritical, e.NewStatus())

	require.Len(t, instances, 1, "first sight of the instance is announced even without registration event")
}

func TestCompareServiceDeregistrationAndCheckRemoval(t *testing.T) {
	w := newTestWatcher()
	marks := map[instanceKey]instanceMark{}
	id1, id2 := "marathon-app-a-1", "marathon-app-a-2"

	old := []consul.CheckServiceNode{
		instance("n1", id1, 1, check("c1", "marathon_http_check_0", api.HealthPassing, id1), check("c1b", "extra", api.HealthPassing, id1)),
		instance("n2", id2, 2, check("c2", "marathon_http_check_0", api.HealthWarning, id2)),
	}
	new := []consul.CheckServiceNode{
		instance("n1", id1, 1, check("c1", "marathon_http_check_0", api.HealthPassing, id1)),
	}
	marks[instanceKey{"n1", id1}] = instanceMark{fingerprint: fingerprint(old[0]), announced: time.Now()}
	marks[instanceKey{"n2", id2}] = instanceMark{fingerprint: fingerprint(old[1]), announced: time.Now()}

	w.compareService(time.Now(), 9, old, new, marks)
	events, instances := w.drain()

	gone := byKind(events, tl.KindInstance)
	require.Len(t, gone, 1)
	require.Equal(t, "n2", gone[0].NodeName)
	require.Equal(t, tl.StatusWarning, gone[0].OldServiceStatus)
	require.Equal(t, tl.StatusMissing, gone[0].NewServiceStatus)
	require.Equal(t, []string{"http"}, gone[0].Tags)
	require.NotContains(t, marks, instanceKey{"n2", id2}, "marks of gone instances are dropped")

	removed := byKind(events, tl.KindCheck)
	require.Len(t, removed, 1)
	require.Equal(t, "c1b", removed[0].CheckID)
	require.Equal(t, tl.StatusMissing, removed[0].NewCheckStatus)
	require.Empty(t, removed[0].CheckOutput)

	require.Empty(t, instances, "unchanged, recently announced instances are not re-sent")
}

func TestAnnounceOnRegistrationChangeAndAge(t *testing.T) {
	w := newTestWatcher()
	marks := map[instanceKey]instanceMark{}
	id := "kubernetes-pod-web-frontend-10.48.1.2-8080"
	csn := instance("n1", id, 8080)
	now := time.Now()

	w.announce(now, csn, marks, true)
	w.announce(now, csn, marks, false)
	_, instances := w.drain()
	require.Len(t, instances, 1)

	changed := instance("n1", id, 8080)
	changed.Service.Meta["version"] = "2"
	w.announce(now, changed, marks, false)
	_, instances = w.drain()
	require.Len(t, instances, 1, "a meta change is announced")
	require.Equal(t, "2", instances[0].Version)

	w.announce(now.Add(instanceRefresh+time.Minute), changed, marks, false)
	_, instances = w.drain()
	require.Len(t, instances, 1, "a day later the unchanged instance is announced again")
}

func TestCompareNode(t *testing.T) {
	w := newTestWatcher()
	node := &consul.Node{Node: "n1", Address: "10.0.0.1"}

	w.compareNode(time.Now(), 3, node, []*consul.HealthCheck{serf(api.HealthPassing)}, []*consul.HealthCheck{serf(api.HealthCritical)})
	events, _ := w.drain()
	require.Len(t, events, 1)
	require.Equal(t, tl.KindNode, events[0].Kind)
	require.Equal(t, "Serf Health Status", events[0].CheckName)
	require.Equal(t, tl.StatusPassing, events[0].OldNodeStatus)
	require.Equal(t, tl.StatusCritical, events[0].NewNodeStatus)
	require.Equal(t, tl.StatusCritical, events[0].NewStatus())

	w.compareNode(time.Now(), 4, node, []*consul.HealthCheck{serf(api.HealthCritical)}, nil)
	events, _ = w.drain()
	require.Len(t, events, 1, "a node leaving the catalog is one event")
	require.Equal(t, tl.StatusMissing, events[0].NewNodeStatus)
	require.Empty(t, events[0].CheckName)
}

func TestAggregate(t *testing.T) {
	require.Equal(t, tl.StatusPassing, instanceStatus(nil), "no checks means passing, as in Consul")
	require.Equal(t, tl.StatusMissing, nodeStatus(nil))
	require.Equal(t, tl.StatusCritical, aggregate([]*consul.HealthCheck{serf(api.HealthPassing), check("c", "c", api.HealthCritical, "s"), check("w", "w", api.HealthWarning, "s")}))
	require.Equal(t, tl.StatusWarning, aggregate([]*consul.HealthCheck{serf(api.HealthPassing), check("w", "w", api.HealthWarning, "s")}))
	require.Equal(t, tl.StatusMaintenance, aggregate([]*consul.HealthCheck{check("c", "c", api.HealthCritical, "s"), {CheckID: api.ServiceMaintPrefix + "s", Status: api.HealthCritical}}))
}
