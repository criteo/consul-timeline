package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

// legacyReader serves rows written by versions before 0.3 from their
// original table, mapped to the v2 model, for times before the first v2
// row. The table has no id and second-precision times, so pages continue
// with a (time, rows already returned at that time) cursor over a
// deterministic ordering.
type legacyReader struct {
	db    *sql.DB
	table string
}

const legacyCols = "time, datacenter, node_name, node_ip, old_node_status, new_node_status, service_name, service_id, " +
	"old_service_status, new_service_status, old_instance_count, new_instance_count, check_name, old_check_status, new_check_status, check_output"

const legacyOrder = " ORDER BY time DESC, node_name, service_id, check_name"

// events returns up to limit legacy events strictly before `before` (the
// cutover), or continuing from cursor when it is set.
func (l *legacyReader) events(ctx context.Context, q storage.Query, before time.Time, cursor storage.Cursor, limit int) ([]tl.Event, storage.Cursor, bool, error) {
	upper, inclusive, skip := before.UTC(), false, 0
	if !cursor.IsZero() {
		upper, inclusive, skip = cursor.Time.UTC(), true, cursor.Skip
	}
	if !q.To.IsZero() && q.To.Before(upper) {
		upper, inclusive, skip = q.To.UTC(), true, 0
	}
	lower := q.From.UTC()
	if lower.Before(minTime) {
		lower = minTime
	}
	if !upper.After(lower) {
		return nil, storage.Cursor{}, false, nil
	}

	conds := []string{"time >= ?"}
	args := []any{lower}
	if inclusive {
		conds = append(conds, "time <= ?")
	} else {
		conds = append(conds, "time < ?")
	}
	args = append(args, upper)
	if q.Datacenter != "" {
		conds = append(conds, "datacenter = ?")
		args = append(args, q.Datacenter)
	}
	for _, f := range q.Filters {
		if f.Not {
			continue
		}
		col := map[string]string{storage.FieldService: "service_name", storage.FieldNode: "node_name", storage.FieldCheck: "check_name"}[f.Field]
		if col == "" {
			continue // applied after mapping, by storage.Match
		}
		var parts []string
		for _, v := range f.Values {
			if strings.HasSuffix(v, "*") {
				parts = append(parts, col+" LIKE ?")
				args = append(args, escapeLike(v[:len(v)-1])+"%")
			} else {
				parts = append(parts, col+" = ?")
				args = append(args, v)
			}
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}
	base := "SELECT " + legacyCols + " FROM `" + l.table + "` WHERE " + strings.Join(conds, " AND ") + legacyOrder + " LIMIT ?, ?"

	// post-filter with the full query semantics but without the bounds we
	// already applied in SQL, so that the cutover exclusion holds
	match := q
	match.From, match.To = time.Time{}, time.Time{}

	var out []tl.Event
	curT, curN := upper, 0
	if inclusive {
		curN = skip
	}
	offset := skip
	hasMore := false
	for round := 0; round < 5 && len(out) < limit; round++ {
		fetch := (limit - len(out)) * 2
		if fetch < 100 {
			fetch = 100
		}
		rows, err := l.db.QueryContext(ctx, base, append(append([]any{}, args...), offset, fetch)...)
		if err != nil {
			return nil, storage.Cursor{}, false, fmt.Errorf("mysql legacy: %w", err)
		}
		fetched, stopped := 0, false
		for rows.Next() {
			e, err := l.scanLegacy(rows)
			if err != nil {
				_ = rows.Close()
				return nil, storage.Cursor{}, false, err
			}
			fetched++
			if len(out) >= limit {
				stopped = true
				break
			}
			offset++
			if !e.Time.Equal(curT) {
				curT, curN = e.Time, 0
			}
			curN++
			if e.Time.Before(before) && storage.Match(e, match) {
				out = append(out, e)
			}
		}
		_ = rows.Close()
		if stopped {
			hasMore = true
			break
		}
		if fetched < fetch {
			break // exhausted
		}
		hasMore = true
	}
	if len(out) < limit && !hasMore {
		return out, storage.Cursor{}, false, nil
	}
	return out, storage.Cursor{Time: curT, ID: 0, Skip: curN}, hasMore, nil
}

func (l *legacyReader) scanLegacy(rows *sql.Rows) (tl.Event, error) {
	var (
		t                                                                sql.NullTime
		dc, node, ip, svc, svcID, check, output                          sql.NullString
		oldNode, newNode, oldSvc, newSvc, oldCnt, newCnt, oldChk, newChk sql.NullInt64
	)
	if err := rows.Scan(&t, &dc, &node, &ip, &oldNode, &newNode, &svc, &svcID, &oldSvc, &newSvc, &oldCnt, &newCnt, &check, &oldChk, &newChk, &output); err != nil {
		return tl.Event{}, err
	}
	e := tl.Event{
		Time:             t.Time,
		Datacenter:       dc.String,
		NodeName:         node.String,
		NodeIP:           ip.String,
		OldNodeStatus:    tl.Status(oldNode.Int64),
		NewNodeStatus:    tl.Status(newNode.Int64),
		ServiceName:      svc.String,
		ServiceID:        svcID.String,
		OldServiceStatus: tl.Status(oldSvc.Int64),
		NewServiceStatus: tl.Status(newSvc.Int64),
		OldHealthy:       int(oldCnt.Int64),
		NewHealthy:       int(newCnt.Int64),
		CheckName:        check.String,
		OldCheckStatus:   tl.Status(oldChk.Int64),
		NewCheckStatus:   tl.Status(newChk.Int64),
		CheckOutput:      output.String,
		Legacy:           true,
	}
	switch {
	case e.ServiceName == "":
		e.Kind = tl.KindNode
	case e.CheckName == "":
		e.Kind = tl.KindInstance
	default:
		e.Kind = tl.KindCheck
	}
	return e, nil
}
