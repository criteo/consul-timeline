-- Draft v2 schema for the partition spike. Not the final migration: the
-- app will own this DDL and manage the daily partitions itself.
--
-- Design points being tested here:
--   * BIGINT id + DATETIME(3): keyset pagination on (time, id), millisecond
--     precision, no more duplicate or skipped rows at page boundaries.
--   * RANGE partitions by day: retention becomes DROP PARTITION, the write
--     path never DELETEs. A MAXVALUE catch-all guarantees inserts never
--     fail; the daily job REORGANIZEs it into tomorrow's partition.
--   * Filter dimensions as indexed columns; everything else as JSON.
--   * PAGE_COMPRESSED to keep check_output and meta cheap on disk.

CREATE TABLE IF NOT EXISTS spike_events_v2 (
  id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  time               DATETIME(3)     NOT NULL,
  dc                 VARCHAR(16)     NOT NULL,
  kind               TINYINT         NOT NULL,           -- 1 check, 2 instance, 3 node
  consul_index       BIGINT UNSIGNED NULL,
  node_name          VARCHAR(255)    NULL,
  node_ip            VARCHAR(45)     NULL,
  node_rack          VARCHAR(32)     NULL,
  old_node_status    TINYINT         NULL,
  new_node_status    TINYINT         NULL,
  service_name       VARCHAR(255)    NULL,
  service_id         VARCHAR(255)    NULL,
  team               VARCHAR(128)    NULL,
  app                VARCHAR(255)    NULL,
  version            VARCHAR(64)     NULL,
  service_tags       JSON            NULL,
  service_meta       JSON            NULL,
  old_service_status TINYINT         NULL,
  new_service_status TINYINT         NULL,
  old_healthy        INT             NULL,
  new_healthy        INT             NULL,
  total_instances    INT             NULL,
  check_id           VARCHAR(255)    NULL,
  check_name         VARCHAR(255)    NULL,
  check_type         VARCHAR(16)     NULL,
  old_check_status   TINYINT         NULL,
  new_check_status   TINYINT         NULL,
  check_output       TEXT            NULL,
  PRIMARY KEY (id, time),
  KEY k_dc_time        (dc, time, id),
  KEY k_dc_service     (dc, service_name, time),
  KEY k_dc_node        (dc, node_name, time),
  KEY k_dc_check       (dc, check_name, time),
  KEY k_dc_team        (dc, team, time),
  KEY k_dc_kind_status (dc, kind, new_check_status, time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 PAGE_COMPRESSED=1
PARTITION BY RANGE (TO_DAYS(time)) (
  PARTITION p20260916 VALUES LESS THAN (TO_DAYS('2026-09-17')),
  PARTITION p20260917 VALUES LESS THAN (TO_DAYS('2026-09-18')),
  PARTITION p_max     VALUES LESS THAN MAXVALUE
);

-- Daily job, step 1: carve tomorrow out of the catch-all (cheap while p_max is empty).
ALTER TABLE spike_events_v2 REORGANIZE PARTITION p_max INTO (
  PARTITION p20260918 VALUES LESS THAN (TO_DAYS('2026-09-19')),
  PARTITION p_max     VALUES LESS THAN MAXVALUE
);

-- Daily job, step 2: retention.
ALTER TABLE spike_events_v2 DROP PARTITION p20260916;

-- A row with JSON, and the shapes of the queries the API will run.
INSERT INTO spike_events_v2 (time, dc, kind, node_name, node_ip, service_name, service_id, team, app, version,
  service_tags, service_meta, old_service_status, new_service_status, old_healthy, new_healthy, total_instances,
  check_id, check_name, check_type, old_check_status, new_check_status, check_output)
VALUES (NOW(3), 'bench', 1, 'bench-node-1', '10.0.0.1', 'web-frontend-admin', 'kubernetes-pod-web-frontend-admin-10.48.1.2-12011',
  'Payments and Billing', 'payments/frontend', '109319',
  JSON_ARRAY('http', 'kubernetes'), JSON_OBJECT('k8s_namespace', 'payments', 'k8s_pod', 'frontend-7869-x'),
  4, 2, 4, 3, 4, 'service:kubernetes-pod-web-frontend-admin-10.48.1.2-12011:2', 'kubernetes_http_check_1', 'http', 4, 2,
  'HTTP GET http://10.48.1.2:12011/admin/traffic: 503 Service Unavailable');

SELECT partition_name, table_rows, data_length, index_length
FROM information_schema.PARTITIONS WHERE table_schema = DATABASE() AND table_name = 'spike_events_v2';

-- Page 1 of a filtered timeline, newest first, pruned to the partitions in range.
EXPLAIN PARTITIONS
SELECT id, time, service_name, check_name, new_check_status FROM spike_events_v2
WHERE dc = 'bench' AND service_name = 'web-frontend-admin'
  AND time BETWEEN NOW() - INTERVAL 1 DAY AND NOW()
ORDER BY time DESC, id DESC LIMIT 200;

-- Page 2: keyset continuation from the last (time, id) of page 1.
EXPLAIN PARTITIONS
SELECT id, time FROM spike_events_v2
WHERE dc = 'bench' AND time >= NOW() - INTERVAL 1 DAY
  AND (time < NOW(3) OR (time = NOW(3) AND id < 1000000))
ORDER BY time DESC, id DESC LIMIT 200;

-- Facets and the histogram are the same scan, aggregated.
EXPLAIN PARTITIONS
SELECT new_check_status, COUNT(*) FROM spike_events_v2
WHERE dc = 'bench' AND time BETWEEN NOW() - INTERVAL 1 DAY AND NOW()
GROUP BY new_check_status;
