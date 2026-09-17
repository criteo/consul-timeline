package consul

// Wire types for the RPC replies this program decodes. They mirror the
// fields of github.com/hashicorp/consul/agent/structs that are actually
// used; msgpack decoding skips everything else. Owning these types keeps
// the decoded data independent of the pinned structs version: checks carry
// Type since Consul 1.9, which the vendored structs package predates.

type QueryMeta struct {
	Index uint64
}

type Node struct {
	ID         string
	Node       string
	Address    string
	Datacenter string
	Meta       map[string]string
}

type NodeService struct {
	Kind    string
	ID      string
	Service string
	Tags    []string
	Address string
	Meta    map[string]string
	Port    int
}

type HealthCheck struct {
	Node        string
	CheckID     string
	Name        string
	Status      string
	Notes       string
	Output      string
	ServiceID   string
	ServiceName string
	Type        string
}

type CheckServiceNode struct {
	Node    *Node
	Service *NodeService
	Checks  []*HealthCheck
}

type IndexedServices struct {
	Services map[string][]string
	QueryMeta
}

type IndexedNodes struct {
	Nodes []*Node
	QueryMeta
}

type IndexedCheckServiceNodes struct {
	Nodes []CheckServiceNode
	QueryMeta
}

type IndexedHealthChecks struct {
	HealthChecks []*HealthCheck
	QueryMeta
}
