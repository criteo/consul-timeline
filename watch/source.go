package watch

import "github.com/criteo/consul-timeline/consul"

// Source is the read side of a Consul datacenter: blocking queries that
// return when the watched object changes or after the server's wait time.
type Source interface {
	Services(idx uint64) (*consul.IndexedServices, error)
	Service(idx uint64, name string) (*consul.IndexedCheckServiceNodes, error)
	Nodes(idx uint64) (*consul.IndexedNodes, error)
	Node(idx uint64, name string) (*consul.IndexedHealthChecks, error)
	Datacenter() string
}
