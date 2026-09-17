// Command bench-loadgen keeps a local Consul cluster busy with
// production-looking services whose health checks change at a target rate
// of timeline events per second. It exists so consul-timeline can be
// exercised at production rates without touching production.
//
// Everything it registers carries the service meta key "bench_loadgen",
// which is how a later run (or -cleanup) finds and removes it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hashicorp/consul/api"
)

const metaMarker = "bench_loadgen"

var (
	agentsFlag  = flag.String("agents", "127.0.0.1:8500", "comma separated Consul agent HTTP addresses to register on")
	familiesN   = flag.Int("families", 40, "application families; each yields 1 to 4 service names")
	podsPerFam  = flag.Int("pods-per-family", 6, "pods (or tasks) per family")
	rate        = flag.Float64("rate", 15, "target timeline events per second")
	flappersN   = flag.Int("flappers", 3, "pods that flap one check every 2 to 4 minutes, like the prod probes")
	seedFlag    = flag.Int64("seed", 1, "random seed")
	workersN    = flag.Int("workers", 8, "concurrent Consul API calls")
	cleanupOnly = flag.Bool("cleanup", false, "deregister everything this tool registered, then exit")
)

type platform int

const (
	k8s platform = iota
	marathon
	lb
)

var (
	teams = []string{"payments", "inventory", "checkout", "pricing", "ranking", "catalog", "identity", "ingest", "network", "adserving", "pacing", "billing", "fleet", "directory", "observability", "storage", "recommend", "search", "gateway", "crm", "ads", "ml", "data", "auth", "edge", "sched", "cache", "stream", "graph", "mail"}
	apps  = []string{"api", "worker", "sched", "gateway", "indexer", "bidder", "scorer", "router", "controller", "exporter", "preferences", "sandbox", "request", "engine", "slave", "config"}
)

// lockedRand makes math/rand safe to share between the producer, the
// workers and the timers that re-register churned pods.
type lockedRand struct {
	mu sync.Mutex
	r  *rand.Rand
}

func (l *lockedRand) Intn(n int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Intn(n)
}

func (l *lockedRand) Float64() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Float64()
}

func (l *lockedRand) hex(n int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[l.Intn(16)]
	}
	return string(b)
}

// between returns a random duration in [lo, hi] seconds.
func (l *lockedRand) between(lo, hi int) time.Duration {
	return time.Duration(lo+l.Intn(hi-lo+1)) * time.Second
}

type family struct {
	team, app string
	plat      platform
	suffixes  []string
}

func (f *family) name() string {
	if f.plat == lb {
		return "lb-in-" + f.team + "-" + f.app
	}
	return f.team + "-" + f.app
}

func newFamily(i int) *family {
	f := &family{team: teams[i%len(teams)], app: apps[(i/len(teams))%len(apps)]}
	if n := i / (len(teams) * len(apps)); n > 0 {
		f.app = fmt.Sprintf("%s%d", f.app, n)
	}
	switch {
	case i%10 < 7:
		f.plat = k8s
		f.suffixes = [][]string{{"", "-admin"}, {"", "-admin", "-remotedbg"}, {"", "-admin", "-remotedbg", "-metricscollector-admin"}}[i%3]
	case i%10 < 9:
		f.plat = marathon
		f.suffixes = [][]string{{""}, {"", "-admin"}}[i%2]
	default:
		f.plat = lb
		f.suffixes = []string{""}
	}
	return f
}

func checkNames(p platform, svc string) []string {
	switch p {
	case k8s:
		return []string{"pod_running", "pod_readiness", "kubernetes_http_check_0", "kubernetes_http_check_1"}
	case marathon:
		return []string{"marathon_http_check_0"}
	default:
		return []string{fmt.Sprintf("Service '%s' check", svc)}
	}
}

type agentConn struct {
	addr string
	node string
	c    *api.Client
}

func connect(addr string) (*agentConn, error) {
	c, err := api.NewClient(&api.Config{Address: addr})
	if err != nil {
		return nil, err
	}
	var self map[string]map[string]interface{}
	for i := 0; ; i++ {
		self, err = c.Agent().Self()
		if err == nil {
			break
		}
		if i >= 60 {
			return nil, fmt.Errorf("agent %s: %w", addr, err)
		}
		time.Sleep(time.Second)
	}
	node, _ := self["Config"]["NodeName"].(string)
	return &agentConn{addr: addr, node: node, c: c}, nil
}

type check struct {
	id, name string
	status   string
}

type service struct {
	name, id string
	port     int
	checks   []*check
}

type pod struct {
	fam     *family
	agent   *agentConn
	ip      string
	name    string
	version string
	svcs    []*service
	flapper bool
}

type gen struct {
	agents []*agentConn
	r      *lockedRand
	jobs   chan func()

	mu   sync.Mutex
	pods []*pod

	actions, events, errs atomic.Int64
}

func (g *gen) newPod(f *family, a *agentConn, version string) *pod {
	p := &pod{fam: f, agent: a, version: version}
	p.ip = fmt.Sprintf("10.%d.%d.%d", 48+g.r.Intn(2), 1+g.r.Intn(200), 2+g.r.Intn(250))
	if f.plat == k8s {
		p.name = fmt.Sprintf("%s-%d-%s-%s", f.app, 7000+g.r.Intn(1000), g.r.hex(10), g.r.hex(5))
	}
	base := 8000 + g.r.Intn(1000)
	if f.plat == marathon {
		base = 31000 + g.r.Intn(1000)
	}
	for i, suf := range f.suffixes {
		s := &service{name: f.name() + suf}
		switch {
		case f.plat == lb:
			s.port = 443
		case suf == "-admin":
			s.port = 12011
		case suf == "-remotedbg":
			s.port = 12013
		case suf == "-metricscollector-admin":
			s.port = 12012
		default:
			s.port = base + i
		}
		switch f.plat {
		case k8s:
			s.id = fmt.Sprintf("kubernetes-pod-%s-%s-%d", s.name, p.ip, s.port)
		case marathon:
			s.id = fmt.Sprintf("marathon-app-%s-%d-%s", s.name, s.port, g.r.hex(32))
		default:
			s.id = s.name // load balancer style: one registration per LB node
		}
		for _, cn := range checkNames(f.plat, s.name) {
			s.checks = append(s.checks, &check{id: s.id + ":" + cn, name: cn})
		}
		p.svcs = append(p.svcs, s)
	}
	return p
}

func tags(f *family) []string {
	switch f.plat {
	case k8s:
		return []string{"http", "kubernetes", "default", f.team}
	case marathon:
		return []string{"http", "marathon", "default", "marathon-user-svc-" + f.team}
	default:
		return []string{"tcp", "lb"}
	}
}

func meta(p *pod) map[string]string {
	m := map[string]string{metaMarker: "1", "team": p.fam.team, "owners": "gu-" + p.fam.team}
	switch p.fam.plat {
	case k8s:
		m["app"] = p.fam.team + "/" + p.fam.app
		m["k8s_namespace"] = p.fam.team
		m["k8s_cluster"] = "bench"
		m["k8s_pod"] = p.name
		m["k8s_controller"] = "ReplicaSet/" + p.name[:strings.LastIndex(p.name, "-")]
		m["deployed_version"] = p.version
		m["version"] = p.version
	case marathon:
		m["marathon_app_id"] = p.fam.team + "/" + p.fam.name()
		m["marathon_app_version"] = p.version
		m["OWNERS"] = "gu-" + p.fam.team
	}
	return m
}

func output(checkName, status, ip string, port int) string {
	url := fmt.Sprintf("http://%s:%d", ip, port)
	switch {
	case checkName == "pod_running":
		if status == api.HealthPassing {
			return "Pod is Running"
		}
		return "Pod is Pending: containers with unready status: [app]"
	case checkName == "pod_readiness":
		if status == api.HealthPassing {
			return "Pod is Ready"
		}
		return "Pod is not Ready: readiness probe failed: HTTP probe failed with statuscode: 503"
	case strings.HasPrefix(checkName, "Service '"):
		if status == api.HealthPassing {
			return fmt.Sprintf("TCP connect %s:%d: Success", ip, port)
		}
		return fmt.Sprintf("dial tcp %s:%d: connect: connection refused", ip, port)
	}
	switch status {
	case api.HealthPassing:
		return "HTTP GET " + url + "/admin/traffic: 200 OK Output: advertise as healthy - accept full traffic"
	case api.HealthWarning:
		// the dash is deliberately non-ASCII, prod outputs contain such characters
		return "HTTP GET " + url + "/admin/traffic: 429 Too Many Requests Output: shedding load – 12% of requests rejected"
	default:
		if port%2 == 0 {
			return "HTTP GET " + url + "/admin/traffic: 503 Service Unavailable Output: advertise as unhealthy - don't accept traffic"
		}
		return fmt.Sprintf("Get %q: dial tcp %s:%d: connect: connection refused", url+"/healthz", ip, port)
	}
}

func (g *gen) registerPod(p *pod, status string) {
	for _, s := range p.svcs {
		reg := &api.AgentServiceRegistration{ID: s.id, Name: s.name, Address: p.ip, Port: s.port, Tags: tags(p.fam), Meta: meta(p)}
		for _, c := range s.checks {
			c.status = status
			reg.Checks = append(reg.Checks, &api.AgentServiceCheck{CheckID: c.id, Name: c.name, TTL: "24h", Status: status})
		}
		if err := p.agent.c.Agent().ServiceRegister(reg); err != nil {
			g.errs.Add(1)
			log.Printf("register %s on %s: %v", s.id, p.agent.addr, err)
		}
	}
}

func (g *gen) deregisterPod(p *pod) {
	for _, s := range p.svcs {
		if err := p.agent.c.Agent().ServiceDeregister(s.id); err != nil {
			g.errs.Add(1)
			log.Printf("deregister %s on %s: %v", s.id, p.agent.addr, err)
		}
	}
}

func (g *gen) setCheck(p *pod, s *service, c *check, status string) {
	if err := p.agent.c.Agent().UpdateTTL(c.id, output(c.name, status, p.ip, s.port), status); err != nil {
		g.errs.Add(1)
		log.Printf("update %s on %s: %v", c.id, p.agent.addr, err)
		return
	}
	c.status = status
}

func (g *gen) addPod(p *pod) {
	g.mu.Lock()
	g.pods = append(g.pods, p)
	g.mu.Unlock()
}

// removePodLocked must be called with g.mu held.
func (g *gen) removePodLocked(p *pod) {
	for i, q := range g.pods {
		if q == p {
			g.pods[i] = g.pods[len(g.pods)-1]
			g.pods = g.pods[:len(g.pods)-1]
			return
		}
	}
}

// pickAction chooses the next thing to do and estimates how many timeline
// events it produces, so the producer can pace to -rate. The mix mirrors
// what prod emits: mostly single check flips, with pod churn contributing
// bursts of registrations and deregistrations.
func (g *gen) pickAction() (int, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.pods) == 0 {
		return 0, nil
	}
	p := g.pods[g.r.Intn(len(g.pods))]
	x := g.r.Float64()
	switch {
	case x < 0.90:
		s := p.svcs[g.r.Intn(len(p.svcs))]
		c := s.checks[g.r.Intn(len(s.checks))]
		next := api.HealthPassing
		if c.status == api.HealthPassing {
			next = api.HealthCritical
			if g.r.Float64() < 0.2 {
				next = api.HealthWarning
			}
		}
		return 1, func() { g.setCheck(p, s, c, next) }
	case x < 0.97 && !p.flapper:
		cost := len(p.svcs)
		for _, s := range p.svcs {
			cost += 3 * len(s.checks)
		}
		g.removePodLocked(p)
		return cost, func() { g.churn(p) }
	default:
		s := p.svcs[g.r.Intn(len(p.svcs))]
		return 2, func() { g.maintenance(p, s) }
	}
}

// churn replaces a pod the way a scheduler does: the old instance goes away,
// a new one appears critical a few seconds later, then turns passing. One
// in ten replacements carries a new version, like a rolling deployment.
func (g *gen) churn(old *pod) {
	g.deregisterPod(old)
	time.AfterFunc(g.r.between(3, 10), func() {
		version := old.version
		if g.r.Float64() < 0.1 {
			version = bump(version)
		}
		np := g.newPod(old.fam, old.agent, version)
		g.registerPod(np, api.HealthCritical)
		g.addPod(np)
		time.AfterFunc(g.r.between(2, 8), func() {
			for _, s := range np.svcs {
				for _, c := range s.checks {
					g.setCheck(np, s, c, api.HealthPassing)
				}
			}
		})
	})
}

func bump(version string) string {
	var n int
	if _, err := fmt.Sscanf(version, "%d", &n); err != nil {
		return version
	}
	return fmt.Sprint(n + 1)
}

func (g *gen) maintenance(p *pod, s *service) {
	if err := p.agent.c.Agent().EnableServiceMaintenance(s.id, "bench: planned maintenance"); err != nil {
		g.errs.Add(1)
		return
	}
	time.AfterFunc(g.r.between(30, 120), func() {
		_ = p.agent.c.Agent().DisableServiceMaintenance(s.id) // the pod may have been churned meanwhile
	})
}

func (g *gen) flap(ctx context.Context, p *pod) {
	s := p.svcs[0]
	c := s.checks[len(s.checks)-1]
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(g.r.between(120, 240)):
		}
		next := api.HealthCritical
		if c.status != api.HealthPassing {
			next = api.HealthPassing
		}
		g.setCheck(p, s, c, next)
		g.events.Add(1)
	}
}

// run paces actions so that the estimated event rate matches -rate.
func (g *gen) run(ctx context.Context) {
	budget := 0.0
	last := time.Now()
	for ctx.Err() == nil {
		now := time.Now()
		budget += now.Sub(last).Seconds() * *rate
		if budget > 3**rate {
			budget = 3 * *rate
		}
		last = now
		cost, job := g.pickAction()
		if job == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if float64(cost) > budget {
			wait := time.Duration((float64(cost) - budget) / *rate * float64(time.Second))
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			budget = 0
			last = time.Now()
		} else {
			budget -= float64(cost)
		}
		g.actions.Add(1)
		g.events.Add(int64(cost))
		select {
		case g.jobs <- job:
		case <-ctx.Done():
			return
		}
	}
}

func (g *gen) stats(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	var lastA, lastE int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a, e := g.actions.Load(), g.events.Load()
		g.mu.Lock()
		n := len(g.pods)
		g.mu.Unlock()
		log.Printf("stats: %.1f actions/s, ~%.1f events/s, %d pods, %d errors", float64(a-lastA)/10, float64(e-lastE)/10, n, g.errs.Load())
		lastA, lastE = a, e
	}
}

func cleanup(agents []*agentConn) int {
	var n atomic.Int64
	var wg sync.WaitGroup
	for _, a := range agents {
		wg.Add(1)
		go func(a *agentConn) {
			defer wg.Done()
			svcs, err := a.c.Agent().Services()
			if err != nil {
				log.Printf("list services on %s: %v", a.addr, err)
				return
			}
			for id, s := range svcs {
				if s.Meta[metaMarker] != "1" {
					continue
				}
				if err := a.c.Agent().ServiceDeregister(id); err != nil {
					log.Printf("deregister %s on %s: %v", id, a.addr, err)
					continue
				}
				n.Add(1)
			}
		}(a)
	}
	wg.Wait()
	return int(n.Load())
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)

	var agents []*agentConn
	for _, addr := range strings.Split(*agentsFlag, ",") {
		a, err := connect(strings.TrimSpace(addr))
		if err != nil {
			log.Fatal(err)
		}
		agents = append(agents, a)
		log.Printf("agent %s is node %s", a.addr, a.node)
	}

	if n := cleanup(agents); n > 0 {
		log.Printf("removed %d services left by a previous run", n)
	}
	if *cleanupOnly {
		return
	}

	g := &gen{agents: agents, r: &lockedRand{r: rand.New(rand.NewSource(*seedFlag))}, jobs: make(chan func(), 1024)}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var workers sync.WaitGroup
	for i := 0; i < *workersN; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range g.jobs {
				job()
			}
		}()
	}

	// initial catalog: pods spread round robin over the agents
	started := time.Now()
	var initial sync.WaitGroup
	pods := 0
	for i := 0; i < *familiesN; i++ {
		f := newFamily(i)
		n := *podsPerFam
		if f.plat == lb && n > len(agents) {
			n = len(agents) // one registration per LB node, like prod
		}
		version := fmt.Sprint(109300 + g.r.Intn(40))
		if f.plat == marathon {
			version = fmt.Sprint(79400 + g.r.Intn(60))
		}
		for j := 0; j < n; j++ {
			p := g.newPod(f, agents[pods%len(agents)], version)
			if f.plat == marathon && pods%17 == 0 && *flappersN > 0 {
				p.flapper = true
				*flappersN--
			}
			g.addPod(p)
			pods++
			initial.Add(1)
			g.jobs <- func() { defer initial.Done(); g.registerPod(p, api.HealthPassing) }
		}
	}
	initial.Wait()
	svcs, checks := 0, 0
	g.mu.Lock()
	for _, p := range g.pods {
		svcs += len(p.svcs)
		for _, s := range p.svcs {
			checks += len(s.checks)
		}
		if p.flapper {
			go g.flap(ctx, p)
		}
	}
	g.mu.Unlock()
	log.Printf("registered %d pods, %d service instances, %d checks on %d agents in %s; targeting %.0f events/s", pods, svcs, checks, len(agents), time.Since(started).Round(time.Millisecond), *rate)

	go g.stats(ctx)
	g.run(ctx)
	close(g.jobs)
	workers.Wait()
	log.Printf("stopped after %d actions (~%d events); registrations are left in place, the next run cleans them up", g.actions.Load(), g.events.Load())
}
