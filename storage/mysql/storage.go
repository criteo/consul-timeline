package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/criteo/consul-timeline/storage"
	tl "github.com/criteo/consul-timeline/timeline"
	_ "github.com/go-sql-driver/mysql"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

var _ storage.Storage = (*Storage)(nil)

type Storage struct {
	cfg Config
	dc  func() string
	db  *sql.DB

	insertStmt *sql.Stmt

	purgeCounter int
}

func New(cfg Config, dc func() string) (*Storage, error) {
	db, err := sql.Open(
		"mysql",
		fmt.Sprintf(
			"%s:%s@tcp(%s:%d)/%s?parseTime=true",
			cfg.User,
			cfg.Password,
			cfg.Host,
			cfg.Port,
			cfg.Database,
		),
	)
	if err != nil {
		return nil, err
	}

	s := &Storage{
		dc:  dc,
		cfg: cfg,
		db:  db,
	}

	if cfg.SetupSchema {
		err := s.setup()
		if err != nil {
			return nil, err
		}
	}

	insertStmt, err := db.Prepare(`
		INSERT INTO events (
			time,
			datacenter,
			node_name,
			node_ip,
			old_node_status,
			new_node_status,
			service_name,
			service_id,
			old_service_status,
			new_service_status,
			old_instance_count,
			new_instance_count,
			check_name,
			old_check_status,
			new_check_status,
			check_output
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?
		)
	`)
	if err != nil {
		return nil, err
	}

	s.insertStmt = insertStmt

	return s, nil
}

func (s *Storage) setup() error {
	log.Info("mysql: setting up schema")
	for _, q := range Schema {
		_, err := s.db.Exec(q)
		if err != nil {
			return err
		}
	}
	log.Info("mysql: schema setup")

	return nil
}

func (s *Storage) Store(evt tl.Event) error {
	if len(evt.CheckOutput) > 2048 {
		evt.CheckOutput = evt.CheckOutput[:2048]
	}
	_, err := s.insertStmt.Exec(
		evt.Time,
		evt.Datacenter,
		evt.NodeName,
		evt.NodeIP,
		evt.OldNodeStatus,
		evt.NewNodeStatus,
		evt.ServiceName,
		evt.ServiceID,
		evt.OldServiceStatus,
		evt.NewServiceStatus,
		evt.OldInstanceCount,
		evt.NewInstanceCount,
		evt.CheckName,
		evt.OldCheckStatus,
		evt.NewCheckStatus,
		evt.CheckOutput,
	)
	if err != nil {
		return errors.Wrap(err, "mysql event insert")
	}

	s.purgeIfNeeded()

	return nil
}

func (s *Storage) Query(ctx context.Context, q storage.Query) (_ []tl.Event, err error) {
	args := []interface{}{}
	qs := `
		SELECT
			time,
			datacenter,
			node_name,
			node_ip,
			old_node_status,
			new_node_status,
			service_name,
			service_id,
			old_service_status,
			new_service_status,
			old_instance_count,
			new_instance_count,
			check_name,
			old_check_status,
			new_check_status,
			check_output
		FROM events
		WHERE
			time <= ? &&
			datacenter = ?
	`
	args = append(args, q.Start)
	args = append(args, s.dc())
	if q.Filter != "" {
		qs += "&& (service_name = ? || node_name = ?) \n"
		args = append(args, q.Filter, q.Filter)
	}
	qs += "ORDER BY `time` DESC\n"
	qs += "LIMIT 0, ?\n"
	args = append(args, q.Limit)

	rows, err := s.db.QueryContext(ctx, qs, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}()

	res := []tl.Event{}
	for rows.Next() {
		evt := tl.Event{}
		err := rows.Scan(
			&evt.Time,
			&evt.Datacenter,
			&evt.NodeName,
			&evt.NodeIP,
			&evt.OldNodeStatus,
			&evt.NewNodeStatus,
			&evt.ServiceName,
			&evt.ServiceID,
			&evt.OldServiceStatus,
			&evt.NewServiceStatus,
			&evt.OldInstanceCount,
			&evt.NewInstanceCount,
			&evt.CheckName,
			&evt.OldCheckStatus,
			&evt.NewCheckStatus,
			&evt.CheckOutput,
		)
		if err != nil {
			return nil, err
		}
		res = append(res, evt)
	}

	return res, nil
}

func (s *Storage) purgeIfNeeded() {
	if s.cfg.PurgeFrequency == 0 || s.cfg.PurgeMaxAgeHours == 0 {
		return
	}

	s.purgeCounter++
	if s.purgeCounter < s.cfg.PurgeFrequency {
		return
	}

	start := time.Now()

	res, err := s.db.Exec(`
		DELETE FROM events
		WHERE time < ?
	`, time.Now().Add(-time.Duration(s.cfg.PurgeMaxAgeHours)*time.Hour))
	if err != nil {
		log.Errorf("mysql: error purging: %s", err)
		return
	}

	s.purgeCounter = 0

	affected, err := res.RowsAffected()
	if err != nil {
		log.Error(err)
		return
	}
	log.Infof("mysql: purged %d events in %s", affected, time.Since(start))
}
