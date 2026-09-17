package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

func TestRingKeepsNewestAndPaginates(t *testing.T) {
	s := New(Config{MaxSize: 3})
	ctx := context.Background()
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	var evs []tl.Event
	for i := 0; i < 4; i++ {
		evs = append(evs, tl.Event{Time: base.Add(time.Duration(i) * time.Second), Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "svc", NewCheckStatus: tl.StatusCritical})
	}
	require.NoError(t, s.StoreEvents(ctx, evs))

	page, err := s.Events(ctx, storage.Query{Datacenter: "dc1", Limit: 2})
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.True(t, page.HasMore)
	require.Equal(t, base.Add(3*time.Second), page.Events[0].Time, "newest first")
	require.Equal(t, base.Add(2*time.Second), page.Events[1].Time)

	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", Limit: 2, Cursor: page.Next})
	require.NoError(t, err)
	require.Len(t, page.Events, 1, "the oldest event fell out of the ring")
	require.False(t, page.HasMore)
	require.Equal(t, base.Add(time.Second), page.Events[0].Time)
}

func TestFiltersFacetsAndSince(t *testing.T) {
	s := New(Config{MaxSize: 10})
	ctx := context.Background()
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.StoreEvents(ctx, []tl.Event{
		{Time: base, Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "web-frontend", CheckName: "http", NewCheckStatus: tl.StatusCritical, Team: "payments", Tags: []string{"http", "kubernetes"}},
		{Time: base.Add(time.Second), Datacenter: "dc1", Kind: tl.KindCheck, ServiceName: "web-frontend-admin", CheckName: "http", NewCheckStatus: tl.StatusPassing, Team: "payments", Tags: []string{"http", "kubernetes"}},
		{Time: base.Add(2 * time.Second), Datacenter: "dc1", Kind: tl.KindInstance, ServiceName: "billing-api", NewServiceStatus: tl.StatusMissing, Team: "storage", Tags: []string{"tcp", "marathon"}},
		{Time: base.Add(3 * time.Second), Datacenter: "dc2", Kind: tl.KindCheck, ServiceName: "web-frontend", CheckName: "http", NewCheckStatus: tl.StatusCritical},
	}))

	page, err := s.Events(ctx, storage.Query{Datacenter: "dc1", Filters: []storage.Filter{{Field: storage.FieldService, Values: []string{"web-frontend*"}}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "prefix match on service, dc1 only")

	page, err = s.Events(ctx, storage.Query{Filters: []storage.Filter{{Field: storage.FieldTo, Values: []string{"critical"}}, {Field: storage.FieldKind, Values: []string{"instance"}, Not: true}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 2, "to:critical across datacenters, excluding instance events")

	page, err = s.Events(ctx, storage.Query{Datacenter: "dc1", Filters: []storage.Filter{{Field: storage.FieldTag, Values: []string{"kube*"}, Not: true}}})
	require.NoError(t, err)
	require.Len(t, page.Events, 1, "negated prefix match on any tag")

	_, err = s.Events(ctx, storage.Query{Filters: []storage.Filter{{Field: "bogus", Values: []string{"x"}}}})
	require.Error(t, err)

	facets, err := s.Facets(ctx, storage.Query{Datacenter: "dc1"}, []string{storage.FieldTeam, storage.FieldTo, storage.FieldTag}, 10)
	require.NoError(t, err)
	require.Equal(t, []storage.FacetValue{{Value: "payments", Count: 2}, {Value: "storage", Count: 1}}, facets.Values[storage.FieldTeam])
	require.Equal(t, []storage.FacetValue{{Value: "http", Count: 2}, {Value: "kubernetes", Count: 2}, {Value: "marathon", Count: 1}, {Value: "tcp", Count: 1}}, facets.Values[storage.FieldTag], "every tag of every event counts")
	require.Equal(t, 3, facets.SampleSize)

	since, err := s.Since(ctx, "dc1", base, 10)
	require.NoError(t, err)
	require.Len(t, since, 2, "strictly after the given time, oldest first")
	require.Equal(t, base.Add(time.Second), since[0].Time)

	dcs, err := s.Datacenters(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"dc1", "dc2"}, dcs)

	names, err := s.Suggest(ctx, "dc1", storage.FieldService, "web", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"web-frontend", "web-frontend-admin"}, names)
	names, err = s.Suggest(ctx, "dc1", storage.FieldTag, "k", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"kubernetes"}, names)
}
