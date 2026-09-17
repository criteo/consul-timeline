package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

const (
	defaultLimit   = 100
	maxLimit       = 1000
	rollupMinRange = 6 * time.Hour
)

// minTime bounds open-ended queries; DATETIME cannot go below year 1000
// and nothing useful happened before 2000 anyway.
var minTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

const selectCols = "id, time, dc, kind, consul_index, node_name, node_ip, old_node_status, new_node_status, " +
	"service_name, service_id, team, app, version, tags, old_service_status, new_service_status, old_healthy, new_healthy, total_instances, " +
	"check_id, check_name, check_type, old_check_status, new_check_status, check_output"

func scanEvent(rows *sql.Rows) (tl.Event, error) {
	var e tl.Event
	var tags string
	err := rows.Scan(&e.ID, &e.Time, &e.Datacenter, &e.Kind, &e.ConsulIndex, &e.NodeName, &e.NodeIP, &e.OldNodeStatus, &e.NewNodeStatus,
		&e.ServiceName, &e.ServiceID, &e.Team, &e.App, &e.Version, &tags, &e.OldServiceStatus, &e.NewServiceStatus, &e.OldHealthy, &e.NewHealthy, &e.TotalInstances,
		&e.CheckID, &e.CheckName, &e.CheckType, &e.OldCheckStatus, &e.NewCheckStatus, &e.CheckOutput)
	if err != nil {
		return e, err
	}
	e.Tags, err = parseTags(tags)
	return e, err
}

// parseTags decodes the tags column; "[]" means none.
func parseTags(s string) ([]string, error) {
	if len(s) <= 2 {
		return nil, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil, fmt.Errorf("tags column: %w", err)
	}
	return tags, nil
}

var columns = map[string]string{
	storage.FieldService:   "service_name",
	storage.FieldNode:      "node_name",
	storage.FieldCheck:     "check_name",
	storage.FieldTeam:      "team",
	storage.FieldApp:       "app",
	storage.FieldVersion:   "version",
	storage.FieldCheckType: "check_type",
	storage.FieldKind:      "kind",
	storage.FieldTag:       "tags",
	storage.FieldTo:        "new_status",
	storage.FieldFrom:      "old_status",
	storage.FieldHealthy:   "new_healthy",
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return defaultLimit
	case n > maxLimit:
		return maxLimit
	}
	return n
}

func bounds(q storage.Query, now time.Time) (from, to time.Time) {
	to = q.To
	if to.IsZero() {
		to = now
	}
	from = q.From
	if from.Before(minTime) {
		from = minTime
	}
	return from.UTC(), to.UTC()
}

// buildWhere renders the datacenter, time bounds, filters and text of a
// query for the events_v2 table.
func buildWhere(q storage.Query, now time.Time) (string, []any) {
	from, to := bounds(q, now)
	conds := []string{"time BETWEEN ? AND ?"}
	args := []any{from, to}
	if q.Datacenter != "" {
		conds = append(conds, "dc = ?")
		args = append(args, q.Datacenter)
	}
	for _, f := range q.Filters {
		sqlf, fargs := filterSQL(f)
		conds = append(conds, sqlf)
		args = append(args, fargs...)
	}
	if q.Text != "" {
		p := "%" + escapeLike(q.Text) + "%"
		conds = append(conds, "(check_output LIKE ? OR service_name LIKE ? OR node_name LIKE ? OR check_name LIKE ?)")
		args = append(args, p, p, p, p)
	}
	return strings.Join(conds, " AND "), args
}

func filterSQL(f storage.Filter) (string, []any) {
	col := columns[f.Field]
	var parts []string
	var args []any
	switch f.Field {
	case storage.FieldKind, storage.FieldTo, storage.FieldFrom, storage.FieldHealthy:
		for _, v := range f.Values {
			parts = append(parts, "?")
			args = append(args, enumValue(f.Field, v))
		}
		sqlf := col + " IN (" + strings.Join(parts, ",") + ")"
		if f.Not {
			sqlf = "NOT " + sqlf
		}
		return sqlf, args
	case storage.FieldTag:
		// tags is a JSON array; JSON_SEARCH takes LIKE patterns with the same escapes
		for _, v := range f.Values {
			if strings.HasSuffix(v, "*") {
				parts = append(parts, "JSON_SEARCH(tags, 'one', ?) IS NOT NULL")
				args = append(args, escapeLike(v[:len(v)-1])+"%")
			} else {
				parts = append(parts, "JSON_CONTAINS(tags, JSON_QUOTE(?))")
				args = append(args, v)
			}
		}
		sqlf := "(" + strings.Join(parts, " OR ") + ")"
		if f.Not {
			sqlf = "NOT " + sqlf
		}
		return sqlf, args
	default:
		for _, v := range f.Values {
			if strings.HasSuffix(v, "*") {
				parts = append(parts, col+" LIKE ?")
				args = append(args, escapeLike(v[:len(v)-1])+"%")
			} else {
				parts = append(parts, col+" = ?")
				args = append(args, v)
			}
		}
		sqlf := "(" + strings.Join(parts, " OR ") + ")"
		if f.Not {
			sqlf = "NOT " + sqlf
		}
		return sqlf, args
	}
}

func enumValue(field, v string) int {
	switch field {
	case storage.FieldKind:
		k, _ := tl.ParseKind(v)
		return int(k)
	case storage.FieldTo, storage.FieldFrom:
		s, _ := tl.ParseStatus(v)
		return int(s)
	default:
		n, _ := strconv.Atoi(v)
		return n
	}
}

// ---- events ---------------------------------------------------------------

func (s *Storage) Events(ctx context.Context, q storage.Query) (storage.Page, error) {
	if err := q.Validate(); err != nil {
		return storage.Page{}, err
	}
	limit := clampLimit(q.Limit)
	now := time.Now()
	var page storage.Page

	if q.Cursor.IsZero() || q.Cursor.ID > 0 {
		where, args := buildWhere(q, now)
		if q.Cursor.ID > 0 {
			where += " AND (time < ? OR (time = ? AND id < ?))"
			args = append(args, q.Cursor.Time.UTC(), q.Cursor.Time.UTC(), q.Cursor.ID)
		}
		args = append(args, limit+1)
		rows, err := s.db.QueryContext(ctx, "SELECT "+selectCols+" FROM events_v2 WHERE "+where+" ORDER BY time DESC, id DESC LIMIT ?", args...)
		if err != nil {
			return page, fmt.Errorf("mysql events: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				return page, err
			}
			page.Events = append(page.Events, e)
		}
		if err := rows.Err(); err != nil {
			return page, err
		}
		if len(page.Events) > limit {
			page.Events = page.Events[:limit]
			last := page.Events[limit-1]
			page.Next = storage.Cursor{Time: last.Time, ID: last.ID}
			page.HasMore = true
			return page, nil
		}
	}

	if s.legacy != nil && len(page.Events) < limit {
		var cursor storage.Cursor
		if !q.Cursor.IsZero() && q.Cursor.ID == 0 {
			cursor = q.Cursor // continuing inside the legacy table
		}
		evs, next, more, err := s.legacy.events(ctx, q, s.cutover(ctx), cursor, limit-len(page.Events))
		if err != nil {
			return page, err
		}
		page.Events = append(page.Events, evs...)
		page.Next = next
		page.HasMore = more
	}
	return page, nil
}

func (s *Storage) Since(ctx context.Context, dc string, after time.Time, limit int) ([]tl.Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+selectCols+" FROM events_v2 WHERE dc = ? AND time > ? ORDER BY time ASC, id ASC LIMIT ?", dc, after.UTC(), clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("mysql since: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []tl.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- histogram ------------------------------------------------------------

func (s *Storage) Histogram(ctx context.Context, q storage.Query, buckets int) ([]storage.Bucket, bool, error) {
	if err := q.Validate(); err != nil {
		return nil, false, err
	}
	now := time.Now()
	from, to := bounds(q, now)
	if from.Equal(minTime) {
		from = to.Add(-time.Hour)
	}
	if buckets <= 0 {
		buckets = 60
	}
	if buckets > 500 {
		buckets = 500
	}
	span := to.Sub(from)
	if span <= 0 {
		return nil, false, nil
	}
	width := (span + time.Duration(buckets) - 1) / time.Duration(buckets)
	width = width.Truncate(time.Second)
	if width < time.Second {
		width = time.Second
	}

	useRollup := len(q.Filters) == 0 && q.Text == "" && span >= rollupMinRange
	if useRollup {
		width = width.Truncate(time.Minute)
		if width < time.Minute {
			width = time.Minute
		}
	}
	n := int((span + width - 1) / width)
	out := make([]storage.Bucket, n)
	for i := range out {
		out[i] = storage.Bucket{Start: from.Add(time.Duration(i) * width), ByStatus: map[tl.Status]int{}}
	}
	add := func(t time.Time, status tl.Status, count int) {
		i := int(t.Sub(from) / width)
		if i < 0 || i >= n {
			return
		}
		out[i].Total += count
		out[i].ByStatus[status] += count
	}

	if useRollup {
		conds, args := "minute BETWEEN ? AND ?", []any{from.Truncate(time.Minute), to}
		if q.Datacenter != "" {
			conds += " AND dc = ?"
			args = append(args, q.Datacenter)
		}
		rows, err := s.db.QueryContext(ctx, "SELECT minute, new_status, SUM(n) FROM events_rollup WHERE "+conds+" GROUP BY minute, new_status", args...)
		if err != nil {
			return nil, false, fmt.Errorf("mysql histogram: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var minute time.Time
			var status tl.Status
			var count int
			if err := rows.Scan(&minute, &status, &count); err != nil {
				return nil, false, err
			}
			add(minute, status, count)
		}
		return out, false, rows.Err()
	}

	where, args := buildWhere(q, now)
	sample := s.sample()
	sqlq := "SELECT FLOOR((UNIX_TIMESTAMP(time) - ?) / ?) AS b, new_status, COUNT(*) FROM (SELECT time, new_status FROM events_v2 WHERE " + where +
		" ORDER BY time DESC LIMIT ?) t GROUP BY b, new_status"
	args = append([]any{from.Unix(), int64(width / time.Second)}, args...)
	args = append(args, sample)
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, false, fmt.Errorf("mysql histogram: %w", err)
	}
	defer func() { _ = rows.Close() }()
	total := 0
	for rows.Next() {
		var b int64
		var status tl.Status
		var count int
		if err := rows.Scan(&b, &status, &count); err != nil {
			return nil, false, err
		}
		total += count
		add(from.Add(time.Duration(b)*width), status, count)
	}
	return out, total >= sample, rows.Err()
}

func (s *Storage) sample() int {
	if s.cfg.FacetSample <= 0 {
		return DefaultConfig.FacetSample
	}
	return s.cfg.FacetSample
}

// ---- facets ---------------------------------------------------------------

func (s *Storage) Facets(ctx context.Context, q storage.Query, fields []string, limit int) (storage.Facets, error) {
	if err := q.Validate(); err != nil {
		return storage.Facets{}, err
	}
	for _, f := range fields {
		if _, ok := columns[f]; !ok || f == storage.FieldFrom || f == storage.FieldHealthy {
			return storage.Facets{}, fmt.Errorf("no facet on %q", f)
		}
	}
	if limit <= 0 {
		limit = 10
	}
	where, args := buildWhere(q, time.Now())
	sample := s.sample()
	args = append(args, sample)
	cols := "kind, new_status, team, app, version, service_name, node_name, check_name, check_type"
	wantTags := slices.Contains(fields, storage.FieldTag)
	if wantTags {
		cols += ", tags" // the one JSON column, fetched only when asked for
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+cols+" FROM events_v2 WHERE "+where+" ORDER BY time DESC LIMIT ?", args...)
	if err != nil {
		return storage.Facets{}, fmt.Errorf("mysql facets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]map[string]int, len(fields))
	for _, f := range fields {
		counts[f] = map[string]int{}
	}
	n := 0
	for rows.Next() {
		var e tl.Event
		var headline tl.Status
		var tags string
		dest := []any{&e.Kind, &headline, &e.Team, &e.App, &e.Version, &e.ServiceName, &e.NodeName, &e.CheckName, &e.CheckType}
		if wantTags {
			dest = append(dest, &tags)
		}
		if err := rows.Scan(dest...); err != nil {
			return storage.Facets{}, err
		}
		if e.Tags, err = parseTags(tags); err != nil {
			return storage.Facets{}, err
		}
		n++
		for _, f := range fields {
			if f == storage.FieldTo {
				counts[f][headline.String()]++ // stored denormalized, independent of kind
				continue
			}
			for _, v := range storage.FieldValues(e, f) {
				counts[f][v]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return storage.Facets{}, err
	}
	res := storage.Facets{Values: make(map[string][]storage.FacetValue, len(fields)), SampleSize: n, Sampled: n >= sample}
	for f, m := range counts {
		res.Values[f] = topValues(m, limit)
	}
	return res, nil
}

func topValues(m map[string]int, limit int) []storage.FacetValue {
	vals := make([]storage.FacetValue, 0, len(m))
	for v, c := range m {
		vals = append(vals, storage.FacetValue{Value: v, Count: c})
	}
	sort.Slice(vals, func(i, j int) bool {
		if vals[i].Count != vals[j].Count {
			return vals[i].Count > vals[j].Count
		}
		return vals[i].Value < vals[j].Value
	})
	if len(vals) > limit {
		vals = vals[:limit]
	}
	return vals
}

// ---- suggest, instances, datacenters ---------------------------------------

func (s *Storage) Suggest(ctx context.Context, dc, field, prefix string, limit int) ([]string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	pattern := escapeLike(prefix) + "%"
	var sqlq string
	args := []any{}
	dcCond := func(col string) string {
		if dc == "" {
			return ""
		}
		args = append(args, dc)
		return col + " = ? AND "
	}
	switch field {
	case storage.FieldService, storage.FieldNode, storage.FieldTeam, storage.FieldApp:
		col := columns[field]
		sqlq = "SELECT DISTINCT " + col + " FROM instances WHERE " + dcCond("dc") + col + " LIKE ? AND " + col + " <> '' ORDER BY " + col + " LIMIT ?"
		args = append(args, pattern, limit)
	case storage.FieldCheck:
		sqlq = "SELECT DISTINCT check_name FROM events_v2 WHERE " + dcCond("dc") + "check_name LIKE ? AND time > ? ORDER BY check_name LIMIT ?"
		args = append(args, pattern, time.Now().Add(-24*time.Hour).UTC(), limit)
	case storage.FieldTag:
		sqlq = "SELECT DISTINCT jt.tag FROM instances i, JSON_TABLE(i.tags, '$[*]' COLUMNS (tag VARCHAR(255) PATH '$')) jt WHERE " + dcCond("i.dc") +
			"i.tags IS NOT NULL AND jt.tag LIKE ? ORDER BY jt.tag LIMIT ?"
		args = append(args, pattern, limit)
	default:
		return nil, fmt.Errorf("no suggestions for %q", field)
	}
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql suggest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Storage) Instance(ctx context.Context, dc, node, serviceID string) (*tl.Instance, error) {
	var in tl.Instance
	var tags, meta, nodeMeta sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT dc, node_name, service_id, service_name, node_ip, address, port, team, app, version, tags, meta, node_meta, first_seen, last_seen FROM instances WHERE dc = ? AND node_name = ? AND service_id = ?", dc, node, serviceID).
		Scan(&in.Datacenter, &in.NodeName, &in.ServiceID, &in.ServiceName, &in.NodeIP, &in.Address, &in.Port, &in.Team, &in.App, &in.Version, &tags, &meta, &nodeMeta, &in.FirstSeen, &in.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mysql instance: %w", err)
	}
	if tags.Valid {
		_ = json.Unmarshal([]byte(tags.String), &in.Tags)
	}
	if meta.Valid {
		_ = json.Unmarshal([]byte(meta.String), &in.Meta)
	}
	if nodeMeta.Valid {
		_ = json.Unmarshal([]byte(nodeMeta.String), &in.NodeMeta)
	}
	return &in, nil
}

func (s *Storage) Datacenters(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT dc FROM instances UNION SELECT DISTINCT dc FROM events_rollup WHERE minute > ?", time.Now().Add(-24*time.Hour).UTC())
	if err != nil {
		return nil, fmt.Errorf("mysql datacenters: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var dc string
		if err := rows.Scan(&dc); err != nil {
			return nil, err
		}
		if !seen[dc] {
			seen[dc] = true
			out = append(out, dc)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}
