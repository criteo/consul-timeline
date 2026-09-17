// Command bench-import copies recent history from a live consul-timeline
// instance into the bench database using the v1 schema. It gives the bench
// real event shapes, real volume, and genuine legacy rows to run the v2
// reader against. It only reads from the source.
//
// The source API paginates by second-precision time with no row id, so
// pages overlap on their boundary second; rows are deduplicated on the
// full tuple to compensate.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	mysqlstore "github.com/criteo/consul-timeline/storage/mysql"
	tl "github.com/criteo/consul-timeline/timeline"
)

var (
	source  = flag.String("source", "", "base URL of a running consul-timeline, e.g. https://consul-timeline.example.net")
	dsn     = flag.String("dsn", "timeline:timeline@tcp(127.0.0.1:3306)/consul_timeline_db?parseTime=true", "MySQL DSN of the bench database")
	since   = flag.Duration("since", 6*time.Hour, "how far back to import")
	page    = flag.Int("page", 5000, "events per request")
	filter  = flag.String("filter", "", "service or node name filter passed to the source")
	dcOver  = flag.String("dc", "", "store rows under this datacenter instead of the source's")
	setup   = flag.Bool("setup-schema", true, "create the v1 schema in the bench database if missing")
	timeout = flag.Duration("timeout", 2*time.Minute, "HTTP timeout per request")
)

const insertSQL = `INSERT INTO events (
	time, datacenter, node_name, node_ip, old_node_status, new_node_status,
	service_name, service_id, old_service_status, new_service_status,
	old_instance_count, new_instance_count,
	check_name, old_check_status, new_check_status, check_output
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)
	if *source == "" {
		log.Fatal("-source is required")
	}

	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("bench database: %v", err)
	}
	if *setup {
		for _, q := range mysqlstore.Schema {
			if _, err := db.Exec(q); err != nil {
				log.Fatalf("schema: %v", err)
			}
		}
	}
	ins, err := db.Prepare(insertSQL)
	if err != nil {
		log.Fatal(err)
	}

	client := &http.Client{Timeout: *timeout}
	start := time.Now().Truncate(time.Second).Add(time.Second)
	until := time.Now().Add(-*since)
	seen := map[string]bool{}
	began := time.Now()
	total := 0
	var oldest time.Time

	for {
		evs, err := fetch(client, start)
		if err != nil {
			log.Fatal(err)
		}
		fresh := make([]tl.Event, 0, len(evs))
		for _, e := range evs {
			if !seen[key(e)] {
				fresh = append(fresh, e)
			}
		}
		if err := insert(db, ins, fresh); err != nil {
			log.Fatal(err)
		}
		total += len(fresh)
		if len(evs) > 0 {
			oldest = evs[len(evs)-1].Time
		}
		log.Printf("%d rows, oldest %s, %.0f rows/s", total, oldest.Format(time.RFC3339), float64(total)/time.Since(began).Seconds())
		if len(evs) < *page || oldest.Before(until) {
			break
		}
		seen = map[string]bool{}
		if oldest.Equal(start) {
			// a whole page inside one second: step back and accept a small gap
			log.Printf("warning: more than %d events at %s, skipping the rest of that second", *page, start.Format(time.RFC3339))
			start = start.Add(-time.Second)
			continue
		}
		for _, e := range evs {
			if e.Time.Equal(oldest) {
				seen[key(e)] = true
			}
		}
		start = oldest
	}
	log.Printf("done: %d rows back to %s in %s", total, oldest.Format(time.RFC3339), time.Since(began).Round(time.Second))
}

func fetch(c *http.Client, start time.Time) ([]tl.Event, error) {
	u := fmt.Sprintf("%s/events?limit=%d&start=%d&filter=%s", strings.TrimRight(*source, "/"), *page, start.Unix(), url.QueryEscape(*filter))
	resp, err := c.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	var evs []tl.Event
	if err := json.NewDecoder(resp.Body).Decode(&evs); err != nil {
		return nil, fmt.Errorf("%s: %w", u, err)
	}
	return evs, nil
}

func key(e tl.Event) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s|%d%d%d%d%d%d%d%d|%s|%s",
		e.Time.Unix(), e.NodeName, e.NodeIP, e.ServiceName, e.ServiceID,
		e.OldNodeStatus, e.NewNodeStatus, e.OldServiceStatus, e.NewServiceStatus,
		e.OldInstanceCount, e.NewInstanceCount, e.OldCheckStatus, e.NewCheckStatus,
		e.CheckName, e.CheckOutput)
}

func insert(db *sql.DB, ins *sql.Stmt, evs []tl.Event) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt := tx.Stmt(ins)
	for _, e := range evs {
		dc := e.Datacenter
		if *dcOver != "" {
			dc = *dcOver
		}
		if _, err := stmt.Exec(
			e.Time, dc, e.NodeName, e.NodeIP, e.OldNodeStatus, e.NewNodeStatus,
			e.ServiceName, e.ServiceID, e.OldServiceStatus, e.NewServiceStatus,
			e.OldInstanceCount, e.NewInstanceCount,
			e.CheckName, e.OldCheckStatus, e.NewCheckStatus, e.CheckOutput,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
