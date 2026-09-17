// Package storage defines how events and instances are persisted and
// queried, independently of the backend.
package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tl "github.com/criteo/consul-timeline/timeline"
)

// Filter fields. Names are what the API and the UI use.
const (
	FieldDatacenter = "dc"
	FieldService    = "service"
	FieldNode       = "node"
	FieldCheck      = "check"
	FieldKind       = "kind"
	FieldTag        = "tag" // any of the instance's service tags
	FieldTeam       = "team"
	FieldApp        = "app"
	FieldVersion    = "version"
	FieldCheckType  = "type"
	FieldTo         = "to"      // headline status after the event
	FieldFrom       = "from"    // headline status before the event
	FieldHealthy    = "healthy" // healthy instances after the event
)

// Fields lists every filterable field.
var Fields = []string{FieldDatacenter, FieldService, FieldNode, FieldCheck, FieldKind, FieldTag, FieldTeam, FieldApp, FieldVersion, FieldCheckType, FieldTo, FieldFrom, FieldHealthy}

// Filter keeps events whose Field equals one of Values. A value ending in
// "*" matches by prefix on name fields. Not inverts the filter.
type Filter struct {
	Field  string
	Values []string
	Not    bool
}

// Query selects events, newest first.
type Query struct {
	Datacenter string // "" means every datacenter
	From, To   time.Time
	Filters    []Filter
	Text       string // substring of the check output, service, node or check name
	Cursor     Cursor // continue after this position
	Limit      int
}

// Cursor is a position in the newest-first ordering: the last row seen. ID
// is 0 for rows without one, in which case Skip counts the rows already
// returned at exactly Time.
type Cursor struct {
	Time time.Time
	ID   int64
	Skip int
}

func (c Cursor) IsZero() bool { return c.Time.IsZero() }

// String encodes the cursor for URLs.
func (c Cursor) String() string {
	if c.IsZero() {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d:%d", c.Time.UnixMilli(), c.ID, c.Skip)))
}

func ParseCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errors.New("malformed cursor")
	}
	parts := strings.Split(string(b), ":")
	if len(parts) != 3 {
		return Cursor{}, errors.New("malformed cursor")
	}
	ms, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseInt(parts[1], 10, 64)
	skip, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return Cursor{}, errors.New("malformed cursor")
	}
	return Cursor{Time: time.UnixMilli(ms).UTC(), ID: id, Skip: skip}, nil
}

// Page is one page of events plus where the next one starts.
type Page struct {
	Events  []tl.Event
	Next    Cursor
	HasMore bool
}

// Split says what a histogram bucket is broken down by.
type Split string

const (
	SplitStatus     Split = "status" // headline status after the event
	SplitDatacenter Split = "dc"
)

// ParseSplit reads the API's split parameter; "" means by status.
func ParseSplit(s string) (Split, error) {
	switch Split(s) {
	case "", SplitStatus:
		return SplitStatus, nil
	case SplitDatacenter:
		return SplitDatacenter, nil
	}
	return "", fmt.Errorf("unknown split %q", s)
}

// Bucket counts the events of one time slot, broken down by status name
// or by datacenter according to the split.
type Bucket struct {
	Start time.Time
	Total int
	By    map[string]int
}

// BucketKey is the value an event counts under for a split.
func BucketKey(e tl.Event, split Split) string {
	if split == SplitDatacenter {
		return e.Datacenter
	}
	return e.NewStatus().String()
}

type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Facets holds the top values per field. When Sampled is set, the counts
// come from the SampleSize most recent matching events only.
type Facets struct {
	Values     map[string][]FacetValue
	SampleSize int
	Sampled    bool
}

type Writer interface {
	StoreEvents(ctx context.Context, events []tl.Event) error
	UpsertInstances(ctx context.Context, instances []tl.Instance) error
}

type Reader interface {
	Events(ctx context.Context, q Query) (Page, error)
	// Histogram splits the query range into about the requested number of
	// buckets, each broken down by split. The boolean reports whether the
	// counts are sampled.
	Histogram(ctx context.Context, q Query, buckets int, split Split) ([]Bucket, bool, error)
	Facets(ctx context.Context, q Query, fields []string, limit int) (Facets, error)
	// Suggest completes a name field (service, node, check, team, app, tag) for one datacenter.
	Suggest(ctx context.Context, dc, field, prefix string, limit int) ([]string, error)
	Instance(ctx context.Context, dc, node, serviceID string) (*tl.Instance, error)
	Datacenters(ctx context.Context) ([]string, error)
	// Since returns the events of one datacenter strictly after a time,
	// oldest first, so a live stream can replay what it missed.
	Since(ctx context.Context, dc string, after time.Time, limit int) ([]tl.Event, error)
}

// Maintainer runs the periodic housekeeping: partitions, retention.
type Maintainer interface {
	Maintain(ctx context.Context) error
}

type Storage interface {
	Writer
	Reader
	Maintainer
}

// ---- filter semantics, shared by every backend and the live stream -------

// Validate checks field names and enum values.
func (q Query) Validate() error {
	for _, f := range q.Filters {
		if err := f.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (f Filter) Validate() error {
	if len(f.Values) == 0 {
		return fmt.Errorf("filter %q has no value", f.Field)
	}
	switch f.Field {
	case FieldDatacenter, FieldService, FieldNode, FieldCheck, FieldTeam, FieldApp, FieldVersion, FieldCheckType, FieldTag:
	case FieldKind:
		for _, v := range f.Values {
			if _, ok := tl.ParseKind(v); !ok {
				return fmt.Errorf("unknown kind %q", v)
			}
		}
	case FieldTo, FieldFrom:
		for _, v := range f.Values {
			if _, ok := tl.ParseStatus(v); !ok {
				return fmt.Errorf("unknown status %q", v)
			}
		}
	case FieldHealthy:
		for _, v := range f.Values {
			if _, err := strconv.Atoi(v); err != nil {
				return fmt.Errorf("healthy wants a number, got %q", v)
			}
		}
	default:
		return fmt.Errorf("unknown filter field %q", f.Field)
	}
	return nil
}

// Match reports whether e satisfies the datacenter, time bounds, filters
// and text of q. The cursor and limit are ignored.
func Match(e tl.Event, q Query) bool {
	if q.Datacenter != "" && e.Datacenter != q.Datacenter {
		return false
	}
	if !q.From.IsZero() && e.Time.Before(q.From) {
		return false
	}
	if !q.To.IsZero() && e.Time.After(q.To) {
		return false
	}
	for _, f := range q.Filters {
		if f.match(e) == f.Not {
			return false
		}
	}
	if q.Text != "" {
		t := strings.ToLower(q.Text)
		if !strings.Contains(strings.ToLower(e.CheckOutput), t) &&
			!strings.Contains(strings.ToLower(e.ServiceName), t) &&
			!strings.Contains(strings.ToLower(e.NodeName), t) &&
			!strings.Contains(strings.ToLower(e.CheckName), t) {
			return false
		}
	}
	return true
}

func (f Filter) match(e tl.Event) bool {
	for _, v := range f.Values {
		if f.matchOne(e, v) {
			return true
		}
	}
	return false
}

func (f Filter) matchOne(e tl.Event, v string) bool {
	switch f.Field {
	case FieldDatacenter:
		return MatchName(e.Datacenter, v)
	case FieldService:
		return MatchName(e.ServiceName, v)
	case FieldNode:
		return MatchName(e.NodeName, v)
	case FieldCheck:
		return MatchName(e.CheckName, v)
	case FieldTeam:
		return MatchName(e.Team, v)
	case FieldApp:
		return MatchName(e.App, v)
	case FieldVersion:
		return MatchName(e.Version, v)
	case FieldCheckType:
		return e.CheckType == v
	case FieldKind:
		return e.Kind.String() == v
	case FieldTag:
		for _, t := range e.Tags {
			if MatchName(t, v) {
				return true
			}
		}
		return false
	case FieldTo:
		return e.NewStatus().String() == v
	case FieldFrom:
		return e.OldStatus().String() == v
	case FieldHealthy:
		n, err := strconv.Atoi(v)
		return err == nil && e.NewHealthy == n
	}
	return false
}

// MatchName compares a name with a filter value, where a trailing "*"
// means prefix.
func MatchName(name, pattern string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	}
	return name == pattern
}

// HeadlineStatus returns the headline old and new status of an event.
func HeadlineStatus(e tl.Event) (old, new tl.Status) {
	return e.OldStatus(), e.NewStatus()
}
