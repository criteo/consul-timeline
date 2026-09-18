// Package mysql stores events in MariaDB or MySQL: a daily partitioned
// event table, an instance registry, and a per-minute rollup for cheap
// histograms over long ranges.
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
)

var _ storage.Storage = (*Storage)(nil)

const (
	maxOutput      = 4096
	insertChunk    = 500
	upsertChunk    = 200
	cutoverRefresh = time.Minute
	maintainLock   = "consul_timeline_maintain"
)

type Storage struct {
	cfg    Config
	db     *sql.DB
	legacy *legacyReader

	cutoverMu   sync.Mutex
	cutoverAt   time.Time
	cutoverTime time.Time
}

// New opens the database and, when configured, creates the schema.
func New(cfg Config) (*Storage, error) {
	db, err := sql.Open("mysql", cfg.dsn())
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
		db.SetMaxIdleConns(cfg.MaxOpenConns / 2)
	}
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("mysql: %w", err)
	}

	s := &Storage{cfg: cfg, db: db}
	if cfg.SetupSchema {
		slog.Info("mysql: setting up schema")
		for _, q := range Schema {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return nil, fmt.Errorf("mysql schema: %w", err)
			}
		}
	}
	if cfg.LegacyTable != "" {
		if !validIdentifier(cfg.LegacyTable) {
			return nil, fmt.Errorf("mysql: invalid legacy table name %q", cfg.LegacyTable)
		}
		s.legacy = &legacyReader{db: db, table: cfg.LegacyTable}
		slog.Info("mysql: serving history from the legacy table too", "table", cfg.LegacyTable)
	}
	return s, nil
}

func (s *Storage) Close() error { return s.db.Close() }

func (cfg Config) dsn() string {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=UTC&charset=utf8mb4&interpolateParams=true",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	if cfg.Params != "" {
		dsn += "&" + cfg.Params
	}
	return dsn
}

func validIdentifier(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !ok {
			return false
		}
	}
	return true
}

// ---- writes ---------------------------------------------------------------

const insertCols = "time, dc, kind, consul_index, node_name, node_ip, old_node_status, new_node_status, " +
	"service_name, service_id, team, app, version, tags, old_service_status, new_service_status, old_healthy, new_healthy, total_instances, " +
	"check_id, check_name, check_type, old_check_status, new_check_status, old_status, new_status, check_output"

const insertColCount = 27

func (s *Storage) StoreEvents(ctx context.Context, events []tl.Event) error {
	for start := 0; start < len(events); start += insertChunk {
		end := start + insertChunk
		if end > len(events) {
			end = len(events)
		}
		if err := s.storeChunk(ctx, events[start:end]); err != nil {
			return fmt.Errorf("mysql insert: %w", err)
		}
	}
	return nil
}

type rollupKey struct {
	dc     string
	minute time.Time
	kind   tl.Kind
	status tl.Status
}

func (s *Storage) storeChunk(ctx context.Context, events []tl.Event) error {
	var sb strings.Builder
	sb.WriteString("INSERT INTO events_v2 (" + insertCols + ") VALUES ")
	args := make([]any, 0, len(events)*insertColCount)
	rollup := map[rollupKey]int{}
	for i, e := range events {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
		output := e.CheckOutput
		if len(output) > maxOutput {
			output = output[:maxOutput]
		}
		args = append(args,
			e.Time.UTC(), e.Datacenter, e.Kind, e.ConsulIndex, e.NodeName, e.NodeIP, e.OldNodeStatus, e.NewNodeStatus,
			e.ServiceName, e.ServiceID, e.Team, e.App, e.Version, tagsJSON(e.Tags), e.OldServiceStatus, e.NewServiceStatus, e.OldHealthy, e.NewHealthy, e.TotalInstances,
			e.CheckID, e.CheckName, e.CheckType, e.OldCheckStatus, e.NewCheckStatus, e.OldStatus(), e.NewStatus(), output,
		)
		rollup[rollupKey{e.Datacenter, e.Time.UTC().Truncate(time.Minute), e.Kind, e.NewStatus()}]++
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return err
	}

	sb.Reset()
	sb.WriteString("INSERT INTO events_rollup (dc, minute, kind, new_status, n) VALUES ")
	rargs := make([]any, 0, len(rollup)*5)
	i := 0
	for k, n := range rollup {
		if i > 0 {
			sb.WriteByte(',')
		}
		i++
		sb.WriteString("(?,?,?,?,?)")
		rargs = append(rargs, k.dc, k.minute, k.kind, k.status, n)
	}
	sb.WriteString(" ON DUPLICATE KEY UPDATE n = n + VALUES(n)")
	if _, err := tx.ExecContext(ctx, sb.String(), rargs...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Storage) UpsertInstances(ctx context.Context, instances []tl.Instance) error {
	for start := 0; start < len(instances); start += upsertChunk {
		end := start + upsertChunk
		if end > len(instances) {
			end = len(instances)
		}
		if err := s.upsertChunk(ctx, instances[start:end]); err != nil {
			return fmt.Errorf("mysql upsert: %w", err)
		}
	}
	return nil
}

func (s *Storage) upsertChunk(ctx context.Context, instances []tl.Instance) error {
	var sb strings.Builder
	sb.WriteString("INSERT INTO instances (dc, node_name, service_id, service_name, node_ip, address, port, team, app, version, tags, meta, node_meta, first_seen, last_seen) VALUES ")
	args := make([]any, 0, len(instances)*15)
	for i, in := range instances {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
		args = append(args, in.Datacenter, in.NodeName, in.ServiceID, in.ServiceName, in.NodeIP, in.Address, in.Port,
			in.Team, in.App, in.Version, jsonOrNull(in.Tags), jsonOrNull(in.Meta), jsonOrNull(in.NodeMeta), in.FirstSeen.UTC(), in.LastSeen.UTC())
	}
	sb.WriteString(" ON DUPLICATE KEY UPDATE service_name = VALUES(service_name), node_ip = VALUES(node_ip), address = VALUES(address), " +
		"port = VALUES(port), team = VALUES(team), app = VALUES(app), version = VALUES(version), " +
		"tags = VALUES(tags), meta = VALUES(meta), node_meta = VALUES(node_meta), last_seen = VALUES(last_seen)")
	_, err := s.db.ExecContext(ctx, sb.String(), args...)
	return err
}

// tagsJSON renders an event's tags for the NOT NULL column.
func tagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func jsonOrNull(v any) any {
	switch x := v.(type) {
	case []string:
		if len(x) == 0 {
			return nil
		}
	case map[string]string:
		if len(x) == 0 {
			return nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// ---- maintenance ----------------------------------------------------------

// Maintain creates the partitions for yesterday, today and tomorrow, drops
// the ones past retention, prunes rollups and instances, and purges the
// legacy table. Instances of every datacenter share the tables, so the
// work is serialized on a MySQL named lock and every step is idempotent.
func (s *Storage) Maintain(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", maintainLock).Scan(&got); err != nil {
		return fmt.Errorf("maintain lock: %w", err)
	}
	if !got.Valid || got.Int64 != 1 {
		return nil // another instance is on it
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", maintainLock) }()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	cutoff := today.AddDate(0, 0, -s.cfg.RetentionDays)

	var errs []error
	if err := s.ensurePartitions(ctx, conn, today); err != nil {
		errs = append(errs, err)
	}
	if err := s.dropExpiredPartitions(ctx, conn, cutoff); err != nil {
		errs = append(errs, err)
	}
	if err := deleteBatches(ctx, conn, "DELETE FROM events_rollup WHERE minute < ? LIMIT 50000", cutoff); err != nil {
		errs = append(errs, fmt.Errorf("rollup retention: %w", err))
	}
	if err := deleteBatches(ctx, conn, "DELETE FROM instances WHERE last_seen < ? LIMIT 10000", cutoff); err != nil {
		errs = append(errs, fmt.Errorf("instances retention: %w", err))
	}
	if s.legacy != nil {
		if err := deleteBatches(ctx, conn, "DELETE FROM `"+s.legacy.table+"` WHERE time < ? LIMIT 50000", cutoff); err != nil {
			errs = append(errs, fmt.Errorf("legacy retention: %w", err))
		}
	}
	return errors.Join(errs...)
}

func partitionName(day time.Time) string { return "p" + day.UTC().Format("20060102") }

func partitionDay(name string) (time.Time, bool) {
	t, err := time.Parse("p20060102", name)
	return t, err == nil
}

func (s *Storage) partitions(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT partition_name FROM information_schema.PARTITIONS WHERE table_schema = DATABASE() AND table_name = 'events_v2' AND partition_name IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names[n] = true
	}
	return names, rows.Err()
}

func (s *Storage) ensurePartitions(ctx context.Context, conn *sql.Conn, today time.Time) error {
	existing, err := s.partitions(ctx, conn)
	if err != nil {
		return fmt.Errorf("list partitions: %w", err)
	}
	if !existing["p_max"] {
		return errors.New("events_v2 has no p_max partition; not touching partitions")
	}
	var latest time.Time
	for n := range existing {
		if d, ok := partitionDay(n); ok && d.After(latest) {
			latest = d
		}
	}
	for _, day := range []time.Time{today.AddDate(0, 0, -1), today, today.AddDate(0, 0, 1)} {
		if existing[partitionName(day)] || !day.After(latest) {
			continue // present, or below an existing bound: rows go to the next partition up
		}
		ddl := fmt.Sprintf("ALTER TABLE events_v2 REORGANIZE PARTITION p_max INTO (PARTITION %s VALUES LESS THAN (TO_DAYS('%s')), PARTITION p_max VALUES LESS THAN MAXVALUE)",
			partitionName(day), day.AddDate(0, 0, 1).Format("2006-01-02"))
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create partition %s: %w", partitionName(day), err)
		}
		slog.Info("mysql: partition created", "partition", partitionName(day))
		latest = day
	}
	return nil
}

func (s *Storage) dropExpiredPartitions(ctx context.Context, conn *sql.Conn, cutoff time.Time) error {
	existing, err := s.partitions(ctx, conn)
	if err != nil {
		return fmt.Errorf("list partitions: %w", err)
	}
	for n := range existing {
		day, ok := partitionDay(n)
		if !ok || day.AddDate(0, 0, 1).After(cutoff) {
			continue // not ours, or still holds rows newer than the cutoff
		}
		if _, err := conn.ExecContext(ctx, "ALTER TABLE events_v2 DROP PARTITION "+n); err != nil {
			return fmt.Errorf("drop partition %s: %w", n, err)
		}
		slog.Info("mysql: partition dropped", "partition", n)
	}
	return nil
}

// deleteBatches runs a LIMITed delete until it affects nothing, bounded
// per call so one maintenance run cannot hog the database.
func deleteBatches(ctx context.Context, conn *sql.Conn, stmt string, arg any) error {
	for i := 0; i < 20; i++ {
		res, err := conn.ExecContext(ctx, stmt, arg)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
	}
	return nil
}

// cutover is the time of the first v2 row; legacy rows at or after it are
// ignored so the two tables never overlap.
func (s *Storage) cutover(ctx context.Context) time.Time {
	s.cutoverMu.Lock()
	defer s.cutoverMu.Unlock()
	if time.Since(s.cutoverAt) < cutoverRefresh {
		return s.cutoverTime
	}
	var t sql.NullTime
	err := s.db.QueryRowContext(ctx, "SELECT time FROM events_v2 ORDER BY id ASC LIMIT 1").Scan(&t)
	switch {
	case err == nil && t.Valid:
		s.cutoverTime = t.Time
	case errors.Is(err, sql.ErrNoRows) || (err == nil && !t.Valid):
		s.cutoverTime = time.Now()
	default:
		slog.Warn("mysql: cutover lookup", "err", err)
		return time.Now()
	}
	s.cutoverAt = time.Now()
	return s.cutoverTime
}
