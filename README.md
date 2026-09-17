# Consul Timeline

Consul Timeline watches every service and node of a Consul datacenter,
records each health transition, and lets you browse them live or back in
time: why a service was unavailable yesterday at 2 AM, when a node
started failing, what a deployment did to its instances.

Each event carries the check, the instance and the node it happened on,
the healthy instance count before and after, the instance's tags, and
the team, application and version read from its meta, so the history can
be filtered by any of them. Registrations themselves (tags, meta,
addresses) are kept once per instance and shown alongside events.

## Running

Consul Timeline is a single binary configured by flags or a YAML/JSON
file (`-config`). It talks to Consul servers on their RPC port, the way an
agent does, after discovering them through the agent given by `-consul`.

### In memory, for a look

```yaml
consul:
  address: localhost:8500
  token: your_acl_token   # if ACLs require one

server:
  listen: :8888
```

The UI is at http://localhost:8888/web/, the API under `/api/v1/`.

### With MySQL or MariaDB

```yaml
storage: mysql

consul:
  address: https://consul-relay.example.net
  datacenter: dc1           # optional, defaults to the agent's own

server:
  listen: :8888

mysql:
  host: localhost
  port: 3306
  user: timeline
  password: secret
  database: consul_timeline
  setup_schema: true        # create the tables at startup
  retention_days: 14        # older daily partitions are dropped
```

Print the schema with `consul-timeline -mysql-print-schema`. Events land in
a table partitioned by day, instances in a registry table, and per-minute
counts in a rollup table that keeps histograms cheap over long ranges.

### Several instances

Run more than one instance for availability. With a Consul lock, only one
of them writes to the database and runs its maintenance; every instance
serves reads and its own live stream.

```yaml
consul:
  address: localhost:8500
  enable_distributed_lock: true
  lock_path: consul_timeline/lock   # needs session and KV write on this path
```

### Upgrading from a version before 0.3

Older versions wrote a flat `events` table. Point the new version at it and
its rows stay readable, marked `legacy` in the API, until retention has
purged them all; then drop the table and the setting.

```yaml
mysql:
  legacy_table: events
```

### Full config reference

Values are defaults.

```yaml
log_level: info             # debug, info, warn, error
log_format: text            # text, json
storage: memory             # memory, mysql

consul:
  address: localhost:8500   # agent address, optional http:// or https://
  token: ""
  datacenter: ""            # datacenter to watch, the agent's own if empty
  enable_distributed_lock: false
  lock_path: consul_timeline/lock

server:
  listen: :8888
  static_dir: ""            # serve the UI from a directory instead of the binary

memory:
  max_size: 10000           # events kept in memory

mysql:
  host: localhost
  port: 3306
  user: ""
  password: ""
  database: consul_timeline
  setup_schema: false
  retention_days: 14
  legacy_table: ""
  facet_sample: 100000      # most recent matching rows scanned for facets
  max_open_conns: 16

# how team, app and version are read off a registration: service meta
# keys, the first key present wins. Also settable with -derive-team,
# -derive-app and -derive-version (comma separated).
derive:
  team: [team, owner, owners]
  app: [app, application]
  version: [version]
```

## API

All endpoints are `GET` and return JSON.

| Endpoint | Purpose |
|---|---|
| `/api/v1/meta` | local datacenter, known datacenters, retention, filter fields |
| `/api/v1/events` | events newest first, `cursor` continues a page |
| `/api/v1/histogram` | event counts per time bucket |
| `/api/v1/facets` | top values per field for the current filters |
| `/api/v1/suggest` | completions for a name field |
| `/api/v1/instance` | registration of one instance (`dc`, `node`, `id`) |
| `/api/v1/stream` | live events for the local datacenter (Server-Sent Events) |

Query parameters shared by events, histogram, facets and stream:

| Parameter | Meaning |
|---|---|
| `dc` | datacenter; `all` or empty for every datacenter |
| `from`, `to` | RFC 3339 or unix seconds/milliseconds; `to=now` |
| `f` | filter `field:value`, repeatable; `-field:value` excludes; a trailing `*` matches a prefix |
| `q` | free text over check output and names |
| `limit`, `cursor` | paging |

Filter fields: `service`, `node`, `check`, `kind` (check, instance, node),
`tag` (any service tag of the instance), `team`, `app`, `version`, `type`
(check type), `to` and `from` (status after and before the event),
`healthy` (healthy instances after).

The stream sends each event's time in milliseconds as the SSE id; a client
reconnecting with `Last-Event-ID` (or `?since=`) receives the events it
missed from storage before the live feed resumes.

Operational endpoints: `/healthz`, `/readyz` (watcher ready and storage
reachable), `/metrics` (Prometheus), `/debug/pprof/`.

## Development

`bench/` has a docker compose environment with MariaDB, a Consul cluster
fed by a load generator, the app, and a history importer; it can point at
a real cluster instead. See [bench/README.md](bench/README.md).

```bash
make ui                                        # build the web UI into public/dist (needs Node 20)
make release                                   # static binary with the UI embedded
go test ./...                                  # unit tests
CT_TEST_MYSQL_HOST=127.0.0.1 go test ./storage/mysql/   # storage tests against the bench database
```

The UI lives in `web/` (React, TypeScript, Vite). `npm run dev` there
serves it with hot reload and proxies `/api` to a consul-timeline running
on port 8888. Without `make ui`, the binary still builds and serves a
notice at `/web/` instead of the interface.
