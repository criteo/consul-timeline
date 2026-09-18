package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

// These tests need a MariaDB. Point them at the bench with
//
//	CT_TEST_MYSQL_HOST=127.0.0.1 go test ./storage/mysql/
//
// They create a throwaway database per test and drop it afterwards.
func testStorage(t *testing.T) (*Storage, context.Context) {
	t.Helper()
	host := os.Getenv("CT_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("set CT_TEST_MYSQL_HOST (and optionally _PORT, _USER, _PASSWORD) to run against a MariaDB")
	}
	port, _ := strconv.Atoi(envOr("CT_TEST_MYSQL_PORT", "3306"))
	user, pass := envOr("CT_TEST_MYSQL_USER", "root"), envOr("CT_TEST_MYSQL_PASSWORD", "root")

	admin, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/", user, pass, host, port))
	require.NoError(t, err)
	name := fmt.Sprintf("ct_test_%d", time.Now().UnixNano()%1e9)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)

	s, err := New(Config{Host: host, Port: port, User: user, Password: pass, Database: name, SetupSchema: true, RetentionDays: 14, LegacyTable: "events", FacetSample: 1000, MaxOpenConns: 4})
	require.NoError(t, err)
	for _, q := range LegacySchema {
		_, err := s.db.Exec(q)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		_, _ = admin.Exec("DROP DATABASE " + name)
		_ = admin.Close()
	})
	return s, context.Background()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func event(at time.Time, dc, svc, check string, status tl.Status) tl.Event {
	return tl.Event{Time: at, Datacenter: dc, Kind: tl.KindCheck, Tags: []string{"http", "kubernetes"}, NodeName: "n1", NodeIP: "10.0.0.1",
		ServiceName: svc, ServiceID: "kubernetes-pod-" + svc + "-10.48.0.1-80", Team: "payments", App: "billing/" + svc, Version: "1",
		OldServiceStatus: tl.StatusPassing, NewServiceStatus: status, OldHealthy: 3, NewHealthy: 2, TotalInstances: 3,
		CheckID: "service:x:1", CheckName: check, CheckType: "http", OldCheckStatus: tl.StatusPassing, NewCheckStatus: status, CheckOutput: "HTTP GET 503"}
}

func TestMySQLEventsPagination(t *testing.T) {
	s, ctx := testStorage(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	var evs []tl.Event
	for i := 0; i < 25; i++ {
		evs = append(evs, event(base.Add(time.Duration(i)*time.Second), "dc1", "svc", "http", tl.StatusCritical))
	}
	// two events sharing the same millisecond, to exercise the id tiebreak
	evs = append(evs, event(base.Add(30*time.Second), "dc1", "svc", "http", tl.StatusPassing), event(base.Add(30*time.Second), "dc1", "svc", "tcp", tl.StatusPassing))
	require.NoError(t, s.StoreEvents(ctx, evs))

	seen := map[int64]bool{}
	var last time.Time
	q := storage.Query{Datacenter: "dc1", From: base.Add(-time.Minute), Limit: 10}
	pages, passing := 0, 0
	for {
		page, err := s.Events(ctx, q)
		require.NoError(t, err)
		pages++
		for _, e := range page.Events {
			require.False(t, seen[e.ID], "event %d returned twice", e.ID)
			seen[e.ID] = true
			require.False(t, !last.IsZero() && e.Time.After(last), "not newest first")
			last = e.Time
			if e.NewStatus() == tl.StatusPassing {
				passing++
			}
		}
		if !page.HasMore {
			break
		}
		q.Cursor = page.Next
	}
	require.Equal(t, 27, len(seen))
	require.Equal(t, 2, passing, "headline status is the check status for check events")
	require.Equal(t, 3, pages)
}

func TestMySQLFiltersFacetsHistogram(t *testing.T) {
	s, ctx := testStorage(t)
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	evs := []tl.Event{
		event(base, "dc1", "web-frontend", "http", tl.StatusCritical),
		event(base.Add(time.Minute), "dc1", "web-frontend-admin", "http", tl.StatusPassing),
		event(base.Add(2*time.Minute), "dc1", "billing-api", "tcp", tl.StatusWarning),
		event(base.Add(3*time.Minute), "dc2", "web-frontend", "http", tl.StatusCritical),
	}
	evs[2].Team, evs[2].Tags = "storage", []string{"tcp", "marathon"}
	inst := event(base.Add(4*time.Minute), "dc1", "billing-api", "", tl.StatusMissing)
	inst.Kind, inst.CheckName, inst.CheckType, inst.Team, inst.Tags = tl.KindInstance, "", "", "storage", []string{"tcp", "marathon"}
	inst.NewCheckStatus, inst.NewServiceStatus = 0, tl.StatusMissing
	evs = append(evs, inst)
	require.NoError(t, s.StoreEvents(ctx, evs))

	from := base.Add(-time.Minute)
	page, err := s.Events(ctx, storage.Query{Datacenter: "dc1", From: from, Filters: []storage.Filter{{Field: storage.FieldService, Values: []string{"web-frontend*"}}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "prefix filter, dc1 only")

	page, err = s.Events(ctx, storage.Query{From: from, Filters: []storage.Filter{{Field: storage.FieldTo, Values: []string{"critical"}}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "to:critical across datacenters")

	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", From: from, Filters: []storage.Filter{{Field: storage.FieldKind, Values: []string{"instance"}}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, tl.StatusMissing, page.Events[0].NewStatus())

	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", From: from, Text: "get 503", Filters: []storage.Filter{{Field: storage.FieldTeam, Values: []string{"storage"}, Not: true}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "text search plus negated team")

	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", From: from, Filters: []storage.Filter{{Field: storage.FieldTag, Values: []string{"marathon"}}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "tag filter: the billing-api check and instance events")
	require.Equal(t, []string{"tcp", "marathon"}, page.Events[0].Tags)
	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", From: from, Filters: []storage.Filter{{Field: storage.FieldTag, Values: []string{"kube*"}, Not: true}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "negated prefix tag filter")

	facets, err := s.Facets(ctx, storage.Query{Datacenter: "dc1", From: from}, []string{storage.FieldTeam, storage.FieldTo, storage.FieldKind, storage.FieldTag}, 10)
	require.NoError(t, err)
	require.Equal(t, 4, facets.SampleSize)
	require.False(t, facets.Sampled)
	require.Equal(t, []storage.FacetValue{{Value: "payments", Count: 2}, {Value: "storage", Count: 2}}, facets.Values[storage.FieldTeam])
	require.Equal(t, []storage.FacetValue{{Value: "check", Count: 3}, {Value: "instance", Count: 1}}, facets.Values[storage.FieldKind])
	require.Equal(t, []storage.FacetValue{{Value: "http", Count: 2}, {Value: "kubernetes", Count: 2}, {Value: "marathon", Count: 2}, {Value: "tcp", Count: 2}}, facets.Values[storage.FieldTag])
	require.Contains(t, facets.Values[storage.FieldTo], storage.FacetValue{Value: "missing", Count: 1})

	buckets, sampled, err := s.Histogram(ctx, storage.Query{Datacenter: "dc1", From: from, To: base.Add(10 * time.Minute)}, 11, storage.SplitStatus)
	require.NoError(t, err)
	require.False(t, sampled)
	total := 0
	for _, b := range buckets {
		total += b.Total
	}
	require.Equal(t, 4, total, "direct histogram counts every dc1 event")

	// the rollup path: no filters, span over six hours
	buckets, _, err = s.Histogram(ctx, storage.Query{Datacenter: "dc1", From: base.Add(-7 * time.Hour), To: base.Add(time.Hour)}, 48, storage.SplitStatus)
	require.NoError(t, err)
	total = 0
	for _, b := range buckets {
		total += b.Total
		require.Equal(t, b.Total, sum(b.By))
	}
	require.Equal(t, 4, total, "rollup histogram agrees with the events")

	// every datacenter, split by datacenter, a dc filter evaluated on the rollup
	buckets, _, err = s.Histogram(ctx, storage.Query{From: base.Add(-7 * time.Hour), To: base.Add(time.Hour), Filters: []storage.Filter{{Field: storage.FieldDatacenter, Values: []string{"dc2"}, Not: true}}}, 48, storage.SplitDatacenter)
	require.NoError(t, err)
	by := map[string]int{}
	for _, b := range buckets {
		for k, n := range b.By {
			by[k] += n
		}
	}
	require.Equal(t, map[string]int{"dc1": 4}, by, "split by datacenter, dc2 excluded")

	facets, err = s.Facets(ctx, storage.Query{From: from}, []string{storage.FieldDatacenter}, 10)
	require.NoError(t, err)
	require.Equal(t, []storage.FacetValue{{Value: "dc1", Count: 4}, {Value: "dc2", Count: 1}}, facets.Values[storage.FieldDatacenter])
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func TestMySQLInstancesSuggestDatacenters(t *testing.T) {
	s, ctx := testStorage(t)
	first := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	in := tl.Instance{Datacenter: "dc1", NodeName: "n1", ServiceID: "kubernetes-pod-web-frontend-10.48.0.1-80", ServiceName: "web-frontend",
		NodeIP: "10.0.0.1", Address: "10.48.0.1", Port: 80, Team: "payments", App: "billing/frontend", Version: "1",
		Tags: []string{"http"}, Meta: map[string]string{"team": "payments"}, NodeMeta: map[string]string{"rack_name": "07.04"}, FirstSeen: first, LastSeen: first}
	require.NoError(t, s.UpsertInstances(ctx, []tl.Instance{in}))
	later := in
	later.Version, later.FirstSeen, later.LastSeen = "2", first.Add(time.Hour), first.Add(time.Hour)
	require.NoError(t, s.UpsertInstances(ctx, []tl.Instance{later}))

	got, err := s.Instance(ctx, "dc1", "n1", in.ServiceID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "2", got.Version, "registration changes are applied")
	require.True(t, got.FirstSeen.Equal(first), "first_seen is preserved")
	require.True(t, got.LastSeen.Equal(first.Add(time.Hour)))
	require.Equal(t, []string{"http"}, got.Tags)
	require.Equal(t, map[string]string{"rack_name": "07.04"}, got.NodeMeta)

	missing, err := s.Instance(ctx, "dc1", "n1", "nope")
	require.NoError(t, err)
	require.Nil(t, missing)

	names, err := s.Suggest(ctx, "dc1", storage.FieldService, "web", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"web-frontend"}, names)
	names, err = s.Suggest(ctx, "dc2", storage.FieldService, "web", 10)
	require.NoError(t, err)
	require.Empty(t, names)

	require.NoError(t, s.StoreEvents(ctx, []tl.Event{event(time.Now(), "dc2", "svc", "http", tl.StatusCritical)}))
	dcs, err := s.Datacenters(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"dc1", "dc2"}, dcs, "instances and recent rollups both count")

	checks, err := s.Suggest(ctx, "dc2", storage.FieldCheck, "ht", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"http"}, checks)
	tags, err := s.Suggest(ctx, "dc1", storage.FieldTag, "ht", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"http"}, tags, "tags come from the registry")
}

func TestMySQLMaintainPartitions(t *testing.T) {
	s, ctx := testStorage(t)
	conn, err := s.db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	// pretend the table was created three weeks ago, then run today's maintenance
	require.NoError(t, s.ensurePartitions(ctx, conn, today.AddDate(0, 0, -21)))
	require.NoError(t, s.Maintain(ctx))

	names, err := s.partitions(ctx, conn)
	require.NoError(t, err)
	require.True(t, names["p_max"])
	for _, d := range []int{-1, 0, 1} {
		require.True(t, names[partitionName(today.AddDate(0, 0, d))], "partition for day %+d", d)
	}
	for _, d := range []int{-22, -21, -20} {
		require.False(t, names[partitionName(today.AddDate(0, 0, d))], "partition for day %+d should be dropped by retention", d)
	}

	// rows land in their day, and yesterday's rows are still there after maintenance
	require.NoError(t, s.StoreEvents(ctx, []tl.Event{event(time.Now().Add(-25*time.Hour), "dc1", "svc", "http", tl.StatusCritical)}))
	var n int
	require.NoError(t, s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events_v2 PARTITION ("+partitionName(today.AddDate(0, 0, -1))+")").Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, s.Maintain(ctx), "maintenance is idempotent")
}

func TestMySQLLegacyContinuation(t *testing.T) {
	s, ctx := testStorage(t)
	legacyBase := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	stmt := "INSERT INTO events (time, datacenter, node_name, node_ip, old_node_status, new_node_status, service_name, service_id, old_service_status, new_service_status, old_instance_count, new_instance_count, check_name, old_check_status, new_check_status, check_output) VALUES (?, 'dc1', ?, '10.0.0.1', 0, 0, ?, ?, 4, 2, 3, 2, ?, 4, 2, 'legacy output')"
	for sec := 0; sec < 3; sec++ {
		for i := 0; i < 4; i++ {
			_, err := s.db.ExecContext(ctx, stmt, legacyBase.Add(time.Duration(sec)*time.Second), fmt.Sprintf("n%d", i), "web-frontend", fmt.Sprintf("kubernetes-pod-web-frontend-10.48.0.%d-80", i), fmt.Sprintf("check_%d", i))
			require.NoError(t, err)
		}
	}
	// one legacy row after the cutover must never show up
	_, err := s.db.ExecContext(ctx, stmt, time.Now().Add(time.Minute), "nX", "web-frontend", "id", "check")
	require.NoError(t, err)

	now := time.Now().Truncate(time.Millisecond)
	require.NoError(t, s.StoreEvents(ctx, []tl.Event{
		event(now.Add(-2*time.Second), "dc1", "web-frontend", "http", tl.StatusCritical),
		event(now.Add(-time.Second), "dc1", "web-frontend", "http", tl.StatusPassing),
		event(now, "dc1", "web-frontend", "http", tl.StatusCritical),
	}))

	q := storage.Query{Datacenter: "dc1", From: legacyBase.Add(-time.Hour), Limit: 5}
	var all []tl.Event
	for pages := 0; pages < 10; pages++ {
		page, err := s.Events(ctx, q)
		require.NoError(t, err)
		all = append(all, page.Events...)
		if !page.HasMore {
			break
		}
		q.Cursor = page.Next
	}
	require.Len(t, all, 15, "3 v2 rows then 12 legacy rows, no duplicates, none after the cutover")
	for i, e := range all {
		require.Equal(t, i >= 3, e.Legacy, "row %d", i)
		if i > 0 {
			require.False(t, e.Time.After(all[i-1].Time), "newest first across the boundary")
		}
	}
	require.Equal(t, tl.KindCheck, all[5].Kind)
	require.Empty(t, all[5].Tags, "legacy rows carry no tags")
	require.Equal(t, 2, all[5].NewHealthy)

	keys := map[string]bool{}
	for _, e := range all[3:] {
		k := e.NodeName + e.CheckName + e.Time.String()
		require.False(t, keys[k], "duplicate legacy row %s", k)
		keys[k] = true
	}

	// the same filter semantics apply to legacy rows
	page, err := s.Events(ctx, storage.Query{Datacenter: "dc1", From: legacyBase.Add(-time.Hour), Limit: 50, Filters: []storage.Filter{{Field: storage.FieldNode, Values: []string{"n2"}}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 3)
	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", From: legacyBase.Add(-time.Hour), Limit: 50, Filters: []storage.Filter{{Field: storage.FieldTo, Values: []string{"critical"}}, {Field: storage.FieldCheck, Values: []string{"check_1"}, Not: true}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2+9, "to:critical keeps 2 v2 rows and 9 of 12 legacy rows")

	// the histogram counts legacy rows too when the legacy table can evaluate the filters
	total := func(q storage.Query) int {
		buckets, _, err := s.Histogram(ctx, q, 20, storage.SplitStatus)
		require.NoError(t, err)
		n := 0
		for _, b := range buckets {
			n += b.Total
		}
		return n
	}
	span := storage.Query{Datacenter: "dc1", From: legacyBase.Add(-time.Hour), To: now.Add(time.Second)}
	require.Equal(t, 15, total(span), "3 v2 rows and 12 legacy rows")
	span.Filters = []storage.Filter{{Field: storage.FieldNode, Values: []string{"n2"}}}
	require.Equal(t, 3, total(span), "node filter pushed down to the legacy table")
	span.Filters = []storage.Filter{{Field: storage.FieldNode, Values: []string{"n2"}, Not: true}}
	require.Equal(t, 3+9, total(span), "negated filters are pushed down too")
	span.Filters = []storage.Filter{{Field: storage.FieldTo, Values: []string{"critical"}}}
	require.Equal(t, 2, total(span), "a filter the legacy table cannot evaluate leaves only v2 rows")

	span.Filters = nil
	buckets, _, err := s.Histogram(ctx, span, 20, storage.SplitDatacenter)
	require.NoError(t, err)
	n := 0
	for _, b := range buckets {
		n += b.By["dc1"]
	}
	require.Equal(t, 15, n, "legacy rows count under their datacenter in a dc split")
}

func TestDSNParams(t *testing.T) {
	cfg := Config{Host: "db", Port: 3306, User: "u", Password: "p", Database: "d", Params: "tls=skip-verify&allowCleartextPasswords=true"}
	want := "u:p@tcp(db:3306)/d?parseTime=true&loc=UTC&charset=utf8mb4&interpolateParams=true&tls=skip-verify&allowCleartextPasswords=true"
	if got := cfg.dsn(); got != want {
		t.Fatalf("dsn = %q, want %q", got, want)
	}
}
