// Package tl defines the timeline event model: what the watcher emits, what
// storage persists and what the API serves.
package tl

import (
	"time"

	"github.com/hashicorp/consul/api"
)

// Status is the health of a check, an instance or a node. Values are stored
// as small integers; they must not be renumbered.
type Status int8

const (
	StatusUnknown Status = iota
	StatusMissing        // does not exist (anymore)
	StatusCritical
	StatusWarning
	StatusPassing
	StatusMaintenance
)

var statusNames = [...]string{"unknown", "missing", "critical", "warning", "passing", "maintenance"}

func (s Status) String() string {
	if s < 0 || int(s) >= len(statusNames) {
		return "unknown"
	}
	return statusNames[s]
}

// ParseStatus accepts the names produced by Status.String.
func ParseStatus(s string) (Status, bool) {
	for i, n := range statusNames {
		if n == s {
			return Status(i), true
		}
	}
	return StatusUnknown, false
}

// StatusFromConsul maps a Consul health check status string.
func StatusFromConsul(s string) Status {
	switch s {
	case api.HealthPassing:
		return StatusPassing
	case api.HealthWarning:
		return StatusWarning
	case api.HealthCritical:
		return StatusCritical
	case api.HealthMaint:
		return StatusMaintenance
	default:
		return StatusUnknown
	}
}

// Kind says what changed. Values are stored; do not renumber.
type Kind int8

const (
	KindUnknown  Kind = iota
	KindCheck         // a health check of a service instance changed status
	KindInstance      // a service instance registered or deregistered
	KindNode          // a node registered, deregistered, or a node-level check changed
)

var kindNames = [...]string{"unknown", "check", "instance", "node"}

func (k Kind) String() string {
	if k < 0 || int(k) >= len(kindNames) {
		return "unknown"
	}
	return kindNames[k]
}

func ParseKind(s string) (Kind, bool) {
	for i, n := range kindNames {
		if n == s {
			return Kind(i), true
		}
	}
	return KindUnknown, false
}

// Event is one transition observed in a datacenter. Exactly one of the
// three groups is the headline, selected by Kind; the others give context
// (a check event still says which instance and node it belongs to).
type Event struct {
	ID          int64     `json:"id,omitempty"` // assigned by storage, 0 on the live path
	Time        time.Time `json:"time"`
	Datacenter  string    `json:"datacenter"`
	Kind        Kind      `json:"kind"`
	ConsulIndex uint64    `json:"consul_index,omitempty"`

	NodeName      string `json:"node_name,omitempty"`
	NodeIP        string `json:"node_ip,omitempty"`
	OldNodeStatus Status `json:"old_node_status,omitempty"`
	NewNodeStatus Status `json:"new_node_status,omitempty"`

	ServiceName      string   `json:"service_name,omitempty"`
	ServiceID        string   `json:"service_id,omitempty"`
	Team             string   `json:"team,omitempty"`
	App              string   `json:"app,omitempty"`
	Version          string   `json:"version,omitempty"`
	Tags             []string `json:"tags,omitempty"` // the instance's service tags at the time
	OldServiceStatus Status   `json:"old_service_status,omitempty"`
	NewServiceStatus Status   `json:"new_service_status,omitempty"`
	OldHealthy       int      `json:"old_healthy"`
	NewHealthy       int      `json:"new_healthy"`
	TotalInstances   int      `json:"total_instances,omitempty"`

	CheckID        string `json:"check_id,omitempty"`
	CheckName      string `json:"check_name,omitempty"`
	CheckType      string `json:"check_type,omitempty"`
	OldCheckStatus Status `json:"old_check_status,omitempty"`
	NewCheckStatus Status `json:"new_check_status,omitempty"`
	CheckOutput    string `json:"check_output,omitempty"`

	// Legacy marks rows read from the previous version's table: they have
	// no id, team, version, check id or type, and second-precision time.
	Legacy bool `json:"legacy,omitempty"`
}

// OldStatus is the headline status before the event, according to Kind.
func (e Event) OldStatus() Status {
	switch e.Kind {
	case KindNode:
		return e.OldNodeStatus
	case KindInstance:
		return e.OldServiceStatus
	default:
		return e.OldCheckStatus
	}
}

// NewStatus is the headline status after the event, according to Kind.
func (e Event) NewStatus() Status {
	switch e.Kind {
	case KindNode:
		return e.NewNodeStatus
	case KindInstance:
		return e.NewServiceStatus
	default:
		return e.NewCheckStatus
	}
}

// Instance is the registration record of a service instance on a node. It
// is stored once and updated when the registration changes, so events only
// need to reference it. Meta is kept here and never on events, where it
// would cost more than everything else combined; tags are small enough to
// ride on every event as well.
type Instance struct {
	Datacenter  string            `json:"datacenter"`
	NodeName    string            `json:"node_name"`
	ServiceID   string            `json:"service_id"`
	ServiceName string            `json:"service_name"`
	NodeIP      string            `json:"node_ip,omitempty"`
	Address     string            `json:"address,omitempty"`
	Port        int               `json:"port,omitempty"`
	Team        string            `json:"team,omitempty"`
	App         string            `json:"app,omitempty"`
	Version     string            `json:"version,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
	NodeMeta    map[string]string `json:"node_meta,omitempty"`
	FirstSeen   time.Time         `json:"first_seen"`
	LastSeen    time.Time         `json:"last_seen"`
}
