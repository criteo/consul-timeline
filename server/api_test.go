package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/criteo/consul-timeline/storage"
	"github.com/criteo/consul-timeline/storage/memory"
	tl "github.com/criteo/consul-timeline/timeline"
)

type fakeWatch struct{ ready chan struct{} }

func (f fakeWatch) Datacenter() string     { return "dc1" }
func (f fakeWatch) Ready() <-chan struct{} { return f.ready }
func (f fakeWatch) ServiceNames() []string { return []string{"svc-a", "svc-live"} }
func (f fakeWatch) NodeNames() []string    { return []string{"n1"} }

var base = time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T) (*httptest.Server, *Hub, *memory.Storage) {
	t.Helper()
	store := memory.New(memory.Config{MaxSize: 100})
	require.NoError(t, store.StoreEvents(context.Background(), []tl.Event{
		{Time: base, Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "svc-a", CheckName: "http", NewCheckStatus: tl.StatusCritical, Team: "payments", Tags: []string{"http", "kubernetes"}, CheckOutput: "HTTP 503"},
		{Time: base.Add(time.Second), Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "svc-b", CheckName: "http", NewCheckStatus: tl.StatusPassing, Team: "payments"},
		{Time: base.Add(2 * time.Second), Datacenter: "dc1", Kind: tl.KindInstance, ServiceName: "svc-a", NewServiceStatus: tl.StatusMissing, Team: "payments"},
		{Time: base.Add(3 * time.Second), Datacenter: "dc2", Kind: tl.KindCheck, ServiceName: "svc-a", CheckName: "http", NewCheckStatus: tl.StatusCritical},
	}))
	ready := make(chan struct{})
	close(ready)
	hub := NewHub()
	srv := New(Config{}, store, fakeWatch{ready}, hub, "test", 14)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, hub, store
}

func getJSON(t *testing.T, ts *httptest.Server, path string, out any) int {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.NoError(t, json.NewDecoder(res.Body).Decode(out))
	return res.StatusCode
}

func TestParseQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/events?dc=all&from=1789600000&to=2026-09-17T13:00:00Z&f=service:a&f=service:b*&f=-check:pod_running&f=to:critical&q=503&limit=50", nil)
	q, err := parseQuery(r)
	require.NoError(t, err)
	require.Equal(t, "", q.Datacenter, "all means every datacenter")
	require.Equal(t, time.Unix(1789600000, 0).UTC(), q.From.UTC(), "unix seconds")
	require.Equal(t, time.Date(2026, 9, 17, 13, 0, 0, 0, time.UTC), q.To.UTC())
	require.Equal(t, "503", q.Text)
	require.Equal(t, 50, q.Limit)
	require.Equal(t, []storage.Filter{
		{Field: "service", Values: []string{"a", "b*"}},
		{Field: "check", Values: []string{"pod_running"}, Not: true},
		{Field: "to", Values: []string{"critical"}},
	}, q.Filters, "same field and sign merge into alternatives")

	for _, bad := range []string{"f=bogus:x", "f=service", "f=to:flapping", "from=yesterday", "limit=many", "cursor=not-a-cursor!"} {
		_, err := parseQuery(httptest.NewRequest("GET", "/api/v1/events?"+bad, nil))
		require.Error(t, err, bad)
	}
	q, err = parseQuery(httptest.NewRequest("GET", "/api/v1/events?from=1789600000123", nil))
	require.NoError(t, err)
	require.Equal(t, int64(1789600000123), q.From.UnixMilli(), "large numbers are milliseconds")
}

func TestEventsFacetsMeta(t *testing.T) {
	ts, _, _ := newTestServer(t)

	var page eventsResponse
	require.Equal(t, 200, getJSON(t, ts, "/api/v1/events?dc=dc1&f=service:svc-a", &page))
	require.Len(t, page.Events, 2)
	require.False(t, page.HasMore)
	require.Equal(t, "svc-a", page.Events[0].ServiceName)
	require.Equal(t, 200, getJSON(t, ts, "/api/v1/events?dc=dc1&f=tag:kube*", &page))
	require.Len(t, page.Events, 1, "tag filters apply to the tags carried by the event")

	var bad map[string]string
	require.Equal(t, 400, getJSON(t, ts, "/api/v1/events?f=nope:x", &bad))
	require.Contains(t, bad["error"], "nope")

	var facets facetsResponse
	require.Equal(t, 200, getJSON(t, ts, "/api/v1/facets?dc=dc1&fields=kind,team", &facets))
	require.Equal(t, 3, facets.SampleSize)
	require.Equal(t, []storage.FacetValue{{Value: "check", Count: 2}, {Value: "instance", Count: 1}}, facets.Facets["kind"])

	var meta metaResponse
	require.Equal(t, 200, getJSON(t, ts, "/api/v1/meta", &meta))
	require.Equal(t, "dc1", meta.LocalDatacenter)
	require.Equal(t, []string{"dc1", "dc2"}, meta.Datacenters)
	require.Equal(t, 14, meta.RetentionDays)
	require.Contains(t, meta.Fields, "healthy")

	var sug map[string][]string
	require.Equal(t, 200, getJSON(t, ts, "/api/v1/suggest?dc=dc1&field=service&q=svc", &sug))
	require.Equal(t, []string{"svc-a", "svc-b", "svc-live"}, sug["values"], "stored names and live catalog, merged and sorted")

	res, err := http.Get(ts.URL + "/api/v1/instance?dc=dc1&node=n1&id=unknown")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, 404, res.StatusCode)

	res, err = http.Get(ts.URL + "/readyz")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
}

func TestStreamReplaysThenFollows(t *testing.T) {
	ts, hub, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	params := url.Values{"dc": {"dc1"}, "f": {"service:svc-a"}, "since": {base.Add(-time.Second).UTC().Format(time.RFC3339)}}
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/stream?"+params.Encode(), nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))

	rd := bufio.NewReader(res.Body)
	var replayed []string
	for {
		line, err := rd.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			replayed = append(replayed, line)
		}
		if line == ": connected\n" {
			break
		}
	}
	require.Len(t, replayed, 2, "the two stored svc-a events are replayed, dc2 and svc-b are not")

	hub.Publish(tl.Event{Time: time.Now(), Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "svc-a", CheckName: "http", NewCheckStatus: tl.StatusPassing})
	hub.Publish(tl.Event{Time: time.Now(), Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "svc-other", CheckName: "http", NewCheckStatus: tl.StatusPassing})
	var live tl.Event
	for {
		line, err := rd.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &live))
			break
		}
	}
	require.Equal(t, "svc-a", live.ServiceName, "only matching live events are delivered")
	require.Equal(t, 1, hub.Clients())
}

func TestStreamRejectsOtherDatacenter(t *testing.T) {
	ts, _, _ := newTestServer(t)
	res, err := http.Get(ts.URL + "/api/v1/stream?dc=dc2")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, 400, res.StatusCode)
}
