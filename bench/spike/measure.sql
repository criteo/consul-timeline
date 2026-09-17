-- Row size spike: how much does one event cost on disk under the v2 schema,
-- with and without service meta, with and without page compression?
--
-- Run after the bench has accumulated rows in the v1 `events` table (load
-- generator or bench-import). Copies those rows into throwaway tables that
-- share the v2 layout and reports bytes per row from information_schema.
-- Numbers are only comparable after the pages have been flushed, hence the
-- ANALYZE and the small sleep before reading them.

DROP TABLE IF EXISTS spike_v2_lean, spike_v2_meta, spike_v2_meta_full, spike_v2_meta_nocomp;

-- lean: filter columns only, no JSON
CREATE TABLE spike_v2_lean LIKE spike_events_v2;
ALTER TABLE spike_v2_lean REMOVE PARTITIONING;
INSERT INTO spike_v2_lean (time, dc, kind, node_name, node_ip, old_node_status, new_node_status,
  service_name, service_id, old_service_status, new_service_status, old_healthy, new_healthy,
  check_name, check_type, old_check_status, new_check_status, check_output)
SELECT time, datacenter, IF(check_name = '', 2, 1), node_name, node_ip, old_node_status, new_node_status,
  service_name, service_id, old_service_status, new_service_status, old_instance_count, new_instance_count,
  check_name, 'http', old_check_status, new_check_status, check_output
FROM events;

-- meta: the selected meta keys we plan to keep (~300 bytes of JSON) plus tags
CREATE TABLE spike_v2_meta LIKE spike_events_v2;
ALTER TABLE spike_v2_meta REMOVE PARTITIONING;
INSERT INTO spike_v2_meta (time, dc, kind, node_name, node_ip, node_rack, old_node_status, new_node_status,
  service_name, service_id, team, app, version, service_tags, service_meta,
  old_service_status, new_service_status, old_healthy, new_healthy, total_instances,
  check_id, check_name, check_type, old_check_status, new_check_status, check_output, consul_index)
SELECT time, datacenter, IF(check_name = '', 2, 1), node_name, node_ip, '07.04', old_node_status, new_node_status,
  service_name, service_id, 'Payments and Billing', CONCAT('team/', service_name), '109319',
  JSON_ARRAY('admin-handler-api', 'default', 'fleet', 'http', 'kubernetes', 'netcore', 'webapps'),
  JSON_OBJECT('k8s_cluster', 'workers-10.dc1.prod', 'k8s_namespace', 'payments', 'k8s_pod', 'preferences-7869-7bbb9c667b-4csv8',
              'k8s_controller', 'ReplicaSet/preferences-7869-7bbb9c667b', 'app', 'payments/preferences',
              'deployed_version', '109319', 'dashboard_url', 'https://dashboards.example.net/application/payments%2Fpreferences/fleet/7869?fleetVersionId=42284',
              'container_image', 'registry.example.net/base/dotnet-application-base-8', 'team', 'Payments and Billing', 'owners', 'payments'),
  old_service_status, new_service_status, old_instance_count, new_instance_count, new_instance_count + 1,
  CONCAT('service:', service_id, ':1'), check_name, 'http', old_check_status, new_check_status, check_output, 4812345
FROM events;

-- meta_full: a whole ServiceMeta as registered for a Kubernetes pod (~1.6 KB of JSON)
CREATE TABLE spike_v2_meta_full LIKE spike_events_v2;
ALTER TABLE spike_v2_meta_full REMOVE PARTITIONING;
INSERT INTO spike_v2_meta_full (time, dc, kind, node_name, node_ip, node_rack, old_node_status, new_node_status,
  service_name, service_id, team, app, version, service_tags, service_meta,
  old_service_status, new_service_status, old_healthy, new_healthy, total_instances,
  check_id, check_name, check_type, old_check_status, new_check_status, check_output, consul_index)
SELECT time, datacenter, IF(check_name = '', 2, 1), node_name, node_ip, '07.04', old_node_status, new_node_status,
  service_name, service_id, 'Payments and Billing', CONCAT('team/', service_name), '109319',
  JSON_ARRAY('admin-handler-api', 'default', 'fleet', 'http', 'kubernetes', 'netcore', 'webapps', 'webapps-eu-scope'),
  JSON_OBJECT('k8s_cluster', 'workers-10.dc1.prod', 'k8s_namespace', 'payments', 'k8s_pod', 'preferences-7869-7bbb9c667b-4csv8',
    'k8s_pod_uid', 'e1797a9a-6407-4582-9884-15b15d4db916', 'k8s_controller', 'ReplicaSet/preferences-7869-7bbb9c667b',
    'k8s_node', 'worker-0871.dc1.example.net', 'k8s_port', 'admin', 'k8s_service_account', 'payments', 'k8s_container', 'app',
    'app', 'payments/preferences', 'team', 'Payments and Billing', 'owners', 'payments', 'tier', 'frontend', 'cost_center', '4410',
    'version', '109319', 'deployed_version', '109319', 'release_name', 'preferences-7869', 'use_release_name', 'true',
    'image', 'registry.example.net/payments/preferences', 'image_version', '0.1.0-109177', 'container_version', '0.1.0-109177',
    'base_image', 'registry.example.net/base/dotnet-application-base-8', 'repository', 'payments/preferences', 'branch', 'main',
    'environment', 'prod', 'region', 'eu', 'runtime', 'dotnet', 'runtime_version', 'net10.0', 'framework', 'aspnetcore',
    'framework_version', '10.0', 'app_type', 'webapi', 'registered_at', '2026-09-17T11:30:17Z', 'registered_with', 'registrar/1.0.470',
    'registrar_version', '1.0.470', 'dashboard_url', 'https://dashboards.example.net/application/payments%2Fpreferences/fleet/7869?fleetVersionId=42284',
    'docs_url', 'https://docs.example.net/payments/preferences', 'oncall', 'payments-oncall', 'chat_channel', 'payments-alerts',
    'alert_threshold', '0.25', 'alert_scope', 'local', 'alert_grace_period', '15m', 'alert_enabled', 'true',
    'fqdn', '10-94-25-177.container.example.net', 'port_name', 'admin', 'pool', 'default', 'tags_csv', 'http,webapps,default'),
  old_service_status, new_service_status, old_instance_count, new_instance_count, new_instance_count + 1,
  CONCAT('service:', service_id, ':1'), check_name, 'http', old_check_status, new_check_status, check_output, 4812345
FROM events;

-- meta without page compression, to see what PAGE_COMPRESSED buys
CREATE TABLE spike_v2_meta_nocomp LIKE spike_v2_meta;
ALTER TABLE spike_v2_meta_nocomp PAGE_COMPRESSED=0;
INSERT INTO spike_v2_meta_nocomp SELECT * FROM spike_v2_meta;

ANALYZE TABLE spike_v2_lean, spike_v2_meta, spike_v2_meta_full, spike_v2_meta_nocomp, events;
DO SLEEP(5);

SELECT table_name, table_rows, data_length, index_length,
  ROUND((data_length + index_length) / GREATEST(table_rows, 1)) AS bytes_per_row
FROM information_schema.TABLES
WHERE table_schema = DATABASE() AND table_name IN ('events', 'spike_v2_lean', 'spike_v2_meta', 'spike_v2_meta_full', 'spike_v2_meta_nocomp')
ORDER BY bytes_per_row;
