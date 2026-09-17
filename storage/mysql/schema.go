package mysql

import (
	"fmt"
	"strings"
)

// Schema creates the v2 tables. events_v2 is partitioned by day so that
// retention is a partition drop; the catch-all p_max guarantees inserts
// never fail and is reorganized into the next day's partition by Maintain.
// Page compression cuts the on-disk size by about four on real rows.
var Schema = []string{
	`CREATE TABLE IF NOT EXISTS events_v2 (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  time               DATETIME(3)     NOT NULL,
  dc                 VARCHAR(32)     NOT NULL,
  kind               TINYINT         NOT NULL,
  consul_index       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  node_name          VARCHAR(255)    NOT NULL DEFAULT '',
  node_ip            VARCHAR(45)     NOT NULL DEFAULT '',
  old_node_status    TINYINT         NOT NULL DEFAULT 0,
  new_node_status    TINYINT         NOT NULL DEFAULT 0,
  service_name       VARCHAR(255)    NOT NULL DEFAULT '',
  service_id         VARCHAR(255)    NOT NULL DEFAULT '',
  team               VARCHAR(128)    NOT NULL DEFAULT '',
  app                VARCHAR(255)    NOT NULL DEFAULT '',
  version            VARCHAR(64)     NOT NULL DEFAULT '',
  tags               JSON            NOT NULL,
  old_service_status TINYINT         NOT NULL DEFAULT 0,
  new_service_status TINYINT         NOT NULL DEFAULT 0,
  old_healthy        INT             NOT NULL DEFAULT 0,
  new_healthy        INT             NOT NULL DEFAULT 0,
  total_instances    INT             NOT NULL DEFAULT 0,
  check_id           VARCHAR(255)    NOT NULL DEFAULT '',
  check_name         VARCHAR(255)    NOT NULL DEFAULT '',
  check_type         VARCHAR(16)     NOT NULL DEFAULT '',
  old_check_status   TINYINT         NOT NULL DEFAULT 0,
  new_check_status   TINYINT         NOT NULL DEFAULT 0,
  old_status         TINYINT         NOT NULL DEFAULT 0,
  new_status         TINYINT         NOT NULL DEFAULT 0,
  check_output       TEXT            NOT NULL,
  PRIMARY KEY (id, time),
  KEY k_dc_time    (dc, time, id),
  KEY k_dc_service (dc, service_name, time, id),
  KEY k_dc_node    (dc, node_name, time, id),
  KEY k_dc_check   (dc, check_name, time, id),
  KEY k_dc_team    (dc, team, time, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 PAGE_COMPRESSED=1
PARTITION BY RANGE (TO_DAYS(time)) (PARTITION p_max VALUES LESS THAN MAXVALUE)`,

	`CREATE TABLE IF NOT EXISTS instances (
  dc           VARCHAR(32)  NOT NULL,
  node_name    VARCHAR(255) NOT NULL,
  service_id   VARCHAR(255) NOT NULL,
  service_name VARCHAR(255) NOT NULL,
  node_ip      VARCHAR(45)  NOT NULL DEFAULT '',
  address      VARCHAR(255) NOT NULL DEFAULT '',
  port         INT          NOT NULL DEFAULT 0,
  team         VARCHAR(128) NOT NULL DEFAULT '',
  app          VARCHAR(255) NOT NULL DEFAULT '',
  version      VARCHAR(64)  NOT NULL DEFAULT '',
  tags         JSON         NULL,
  meta         JSON         NULL,
  node_meta    JSON         NULL,
  first_seen   DATETIME(3)  NOT NULL,
  last_seen    DATETIME(3)  NOT NULL,
  PRIMARY KEY (dc, node_name, service_id),
  KEY k_dc_service (dc, service_name),
  KEY k_last_seen  (last_seen)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 PAGE_COMPRESSED=1`,

	`CREATE TABLE IF NOT EXISTS events_rollup (
  dc         VARCHAR(32)  NOT NULL,
  minute     DATETIME     NOT NULL,
  kind       TINYINT      NOT NULL,
  new_status TINYINT      NOT NULL,
  n          INT UNSIGNED NOT NULL,
  PRIMARY KEY (dc, minute, kind, new_status)
) ENGINE=InnoDB`,
}

// LegacySchema is the table written by versions before 0.3. It is kept so
// the bench can create it and so the legacy reader knows its shape.
var LegacySchema = []string{
	`CREATE TABLE IF NOT EXISTS events (
    time DATETIME,
    datacenter VARCHAR(50),
    node_name VARCHAR(255),
    node_ip VARCHAR(45),
    old_node_status TINYINT,
    new_node_status TINYINT,
    service_name VARCHAR(255),
    service_id VARCHAR(255),
    old_service_status TINYINT,
    new_service_status TINYINT,
    old_instance_count INT,
    new_instance_count INT,
    check_name  VARCHAR(255),
    old_check_status TINYINT,
    new_check_status TINYINT,
    check_output VARCHAR(2048)
) CHARSET=utf8`,
	"CREATE INDEX IF NOT EXISTS time_idx ON events (`time` DESC)",
	"CREATE INDEX IF NOT EXISTS time_service_idx ON events (`time` DESC, `service_name`)",
	"CREATE INDEX IF NOT EXISTS time_node_idx ON events (`time` DESC, `node_name`)",
	"CREATE INDEX IF NOT EXISTS time_node_service_idx ON events (`time` DESC, `service_name`, `node_name`)",
}

func PrintSchema() {
	fmt.Println(strings.Join(Schema, ";\n\n") + ";")
}
