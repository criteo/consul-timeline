// Package consul talks to the Consul servers of one datacenter over their
// RPC port, the way an agent does, so that thousands of blocking watches
// cost one multiplexed connection per server instead of one HTTP
// connection each.
package consul

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/consul/agent/pool"
	"github.com/hashicorp/consul/agent/structs"
	"github.com/hashicorp/consul/api"
)

const (
	blockingWait = 10 * time.Minute
	rpcVersion   = 3
)

type Consul struct {
	config   Config
	connPool *pool.ConnPool
	client   *api.Client

	dc          string
	servers     atomic.Value // []net.Addr
	serverIndex uint64

	ready     chan struct{}
	readyOnce sync.Once
}

// New connects to the agent at cfg.Address to discover the servers of the
// datacenter (cfg.Datacenter, or the agent's own) and then talks to them
// directly. It returns before discovery completes; RPC methods block until
// servers are known.
func New(cfg Config) (*Consul, error) {
	client, err := api.NewClient(&api.Config{
		Address: cfg.Address,
		Token:   cfg.Token,
	})
	if err != nil {
		return nil, fmt.Errorf("consul client: %w", err)
	}
	c := &Consul{
		config: cfg,
		connPool: &pool.ConnPool{
			LogOutput:  os.Stderr,
			MaxTime:    30 * time.Second,
			MaxStreams: 50,
		},
		client: client,
		ready:  make(chan struct{}),
	}
	go c.watchServers()
	return c, nil
}

func (c *Consul) Services(idx uint64) (*IndexedServices, error) {
	<-c.ready
	out := &IndexedServices{}
	err := c.rpc("Catalog.ListServices", &structs.DCSpecificRequest{
		Datacenter:   c.dc,
		QueryOptions: c.queryOptions(idx),
	}, out)
	return out, err
}

func (c *Consul) Service(idx uint64, name string) (*IndexedCheckServiceNodes, error) {
	<-c.ready
	out := &IndexedCheckServiceNodes{}
	err := c.rpc("Health.ServiceNodes", &structs.ServiceSpecificRequest{
		Datacenter:   c.dc,
		ServiceName:  name,
		QueryOptions: c.queryOptions(idx),
	}, out)
	return out, err
}

func (c *Consul) Nodes(idx uint64) (*IndexedNodes, error) {
	<-c.ready
	out := &IndexedNodes{}
	err := c.rpc("Catalog.ListNodes", &structs.DCSpecificRequest{
		Datacenter:   c.dc,
		QueryOptions: c.queryOptions(idx),
	}, out)
	return out, err
}

func (c *Consul) Node(idx uint64, name string) (*IndexedHealthChecks, error) {
	<-c.ready
	out := &IndexedHealthChecks{}
	err := c.rpc("Health.NodeChecks", &structs.NodeSpecificRequest{
		Datacenter:   c.dc,
		Node:         name,
		QueryOptions: c.queryOptions(idx),
	}, out)
	return out, err
}

// Datacenter blocks until the servers are discovered.
func (c *Consul) Datacenter() string {
	<-c.ready
	return c.dc
}

// Ready is closed once servers are known.
func (c *Consul) Ready() <-chan struct{} {
	return c.ready
}

func (c *Consul) Lock() (*api.Lock, error) {
	return c.client.LockOpts(&api.LockOptions{
		SessionTTL: (10 * time.Second).String(),
		Key:        c.config.LockPath,
	})
}

func (c *Consul) queryOptions(idx uint64) structs.QueryOptions {
	return structs.QueryOptions{
		Token:         c.config.Token,
		MinQueryIndex: idx,
		MaxQueryTime:  blockingWait,
	}
}

func (c *Consul) watchServers() {
	var idx uint64
	for {
		instances, meta, err := c.client.Health().Service("consul", "", true, &api.QueryOptions{
			Datacenter: c.config.Datacenter,
			WaitIndex:  idx,
			WaitTime:   blockingWait,
		})
		if err != nil {
			slog.Error("consul: retrieving servers", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(instances) == 0 {
			slog.Warn("consul: no passing server in the catalog", "datacenter", c.config.Datacenter)
			time.Sleep(time.Second)
			continue
		}

		servers := make([]net.Addr, 0, len(instances))
		for _, i := range instances {
			servers = append(servers, &net.TCPAddr{
				IP:   net.ParseIP(i.Node.Address),
				Port: i.Service.Port,
			})
		}
		c.servers.Store(servers)
		c.readyOnce.Do(func() {
			c.dc = instances[0].Node.Datacenter
			slog.Info("consul: servers discovered", "datacenter", c.dc, "servers", len(servers))
			close(c.ready)
		})
		idx = meta.LastIndex
	}
}

func (c *Consul) rpc(method string, in, out interface{}) error {
	servers := c.servers.Load().([]net.Addr)
	idx := atomic.AddUint64(&c.serverIndex, 1)
	server := servers[int(idx%uint64(len(servers)))]
	err := c.connPool.RPC(c.dc, server, rpcVersion, method, false, in, out)
	if err != nil {
		rpcErrorCounter.Inc()
		return err
	}
	rpcSuccessCounter.Inc()
	return nil
}
