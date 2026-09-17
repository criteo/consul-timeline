package tl

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeriveApply(t *testing.T) {
	team, app, version := DefaultDerive.Apply(map[string]string{"team": "payments", "owner": "gu-pay", "application": "billing/api", "version": "42"})
	require.Equal(t, "payments", team)
	require.Equal(t, "billing/api", app)
	require.Equal(t, "42", version)

	team, app, version = DefaultDerive.Apply(nil)
	require.Empty(t, team+app+version)

	team, _, _ = Derive{Team: []string{"squad"}}.Apply(map[string]string{"team": "x", "squad": "y"})
	require.Equal(t, "y", team)

	require.Equal(t, []string{"a", "b"}, SplitList(" a, ,b"))
}

func TestEnumRoundTrips(t *testing.T) {
	for s := StatusUnknown; s <= StatusMaintenance; s++ {
		got, ok := ParseStatus(s.String())
		require.True(t, ok)
		require.Equal(t, s, got)
	}
	for k := KindUnknown; k <= KindNode; k++ {
		got, ok := ParseKind(k.String())
		require.True(t, ok)
		require.Equal(t, k, got)
	}
	_, ok := ParseStatus("flapping")
	require.False(t, ok)
	require.Equal(t, "unknown", Status(42).String())
}

func TestHeadlineStatus(t *testing.T) {
	e := Event{Kind: KindCheck, OldCheckStatus: StatusPassing, NewCheckStatus: StatusCritical,
		OldServiceStatus: StatusPassing, NewServiceStatus: StatusWarning, OldNodeStatus: StatusPassing, NewNodeStatus: StatusMissing}
	require.Equal(t, StatusCritical, e.NewStatus())
	e.Kind = KindInstance
	require.Equal(t, StatusWarning, e.NewStatus())
	e.Kind = KindNode
	require.Equal(t, StatusMissing, e.NewStatus())
	require.Equal(t, StatusPassing, e.OldStatus())
}

func TestEventJSONKeepsV1Names(t *testing.T) {
	e := Event{Time: time.Date(2026, 9, 17, 11, 30, 34, 120e6, time.UTC), Datacenter: "dc1", Kind: KindCheck,
		NodeName: "node-1", ServiceName: "web-frontend", CheckName: "http",
		OldCheckStatus: StatusPassing, NewCheckStatus: StatusCritical, CheckOutput: "503"}
	b, err := json.Marshal(e)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	for _, k := range []string{"time", "datacenter", "node_name", "service_name", "check_name", "old_check_status", "new_check_status", "check_output", "kind", "old_healthy", "new_healthy"} {
		require.Contains(t, m, k)
	}
	require.Equal(t, "2026-09-17T11:30:34.12Z", m["time"])
	require.NotContains(t, m, "id", "unstored events carry no id")
	require.NotContains(t, m, "tags", "an event without tags omits them")
}
