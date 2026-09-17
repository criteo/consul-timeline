# Bench

A local environment to develop and load-test consul-timeline: MariaDB
10.11, a small Consul cluster (one server, two client agents) kept busy by
a load generator, and the app built from this checkout.

## Run

```bash
make bench-up        # MariaDB, Consul cluster, load generator, app -> http://localhost:8888/web/
make bench-logs
make bench-down      # stops everything and deletes the database volume
```

| What | Where |
|---|---|
| Timeline UI | http://localhost:8888/web/ |
| Timeline metrics | http://localhost:8888/metrics |
| Consul UI | http://localhost:8500/ui/ |
| MariaDB | `127.0.0.1:3306`, user `timeline` / `timeline`, db `consul_timeline_db` |

Sizing knobs for the load generator, all optional:

```bash
RATE=150 make bench-up                       # ten times a busy datacenter
FAMILIES=800 PODS=4 RATE=80 make bench-up    # ~2,400 service names, ~40k checks
```

`RATE` is the target number of timeline events per second. Compare it with
the app's own counter:

```bash
curl -s localhost:8888/metrics | grep consul_timeline_events_total
```

## Watching a real cluster instead

Copy `bench/.env.example` to `bench/.env` and set `CONSUL_ADDR` (plus
`CONSUL_TOKEN` and `CONSUL_DATACENTER` when needed, and the `DERIVE_*`
variables for that site's meta keys and id conventions). The Makefile
passes the file to compose, so `make bench-up` then runs the app against that cluster;
the local Consul containers still start and can be ignored, or start only
what you need:

```bash
docker compose -f bench/compose.yaml --env-file bench/.env up -d --build mariadb timeline
```

The watcher talks to the cluster's servers on their RPC port after
discovering them through `CONSUL_ADDR`, exactly like a deployed instance,
so the machine running the bench needs that port open.

## What the load generator does

It registers pods and tasks that look like a real fleet: `team-app`
families with `-admin`, `-remotedbg` and `-metricscollector-admin`
siblings, Kubernetes, Marathon and load-balancer style instance ids,
realistic service meta and tags, and typical check names. Checks are TTL
checks so their status can be flipped instantly; the check type therefore
shows as `ttl`, the output text mimics real HTTP, TCP and pod checks.

Every second it spends `RATE` estimated events on a mix of single check
flips, pod churn (deregister, register critical, turn passing, sometimes
with a new version) and service maintenance windows. A few Marathon tasks
flap one check every 2 to 4 minutes, like real network probes do.

Registrations carry the meta key `bench_loadgen`; a new run removes those
left by a previous one. `bench-loadgen -cleanup` removes them and exits.

## Scenarios you trigger by hand

```bash
docker compose -f bench/compose.yaml stop consul-agent-2      # node failure: serf critical, then deregistered
docker compose -f bench/compose.yaml start consul-agent-2     # node comes back
docker compose -f bench/compose.yaml exec consul-agent-1 consul maint -enable -reason=bench   # node maintenance
docker compose -f bench/compose.yaml exec consul-agent-1 consul maint -disable
docker compose -f bench/compose.yaml restart timeline         # app restart: leader lock, watch resync
```

## Real history

Import recent events from a live instance of the previous version into
the bench database. The source is read only, the rows land in the v1
`events` table exactly as that version stores them, so the legacy reader
can be tested on genuine data:

```bash
go run ./bench/import -source https://consul-timeline.example.net -since 6h
```

Six hours of a busy datacenter is a few hundred thousand rows and takes
about a minute. Rows keep their real datacenter; `-dc bench` relabels them
to the local cluster's.

## Running the app on the host

```bash
go run . -consul 127.0.0.1:8500 -storage mysql -mysql-host 127.0.0.1 -mysql-user timeline -mysql-password timeline -mysql-db consul_timeline_db -mysql-setup-schema -mysql-legacy-table events -listen :8889
```

Consul servers are discovered from the catalog and reached on their
container IP, which Linux routes directly.

## Spike notes

`spike/` holds the schema experiments run on this bench: `events_v2.sql`
exercises daily partitions, JSON columns and page compression on a draft
of the v2 table; `measure.sql` copies the v1 rows into candidate layouts
and reports bytes per row. Both work on tables named `spike_*`, never on
the tables the app owns.
