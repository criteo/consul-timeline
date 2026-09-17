# Consul Timeline

Consul Timeline listen to all the events occuring on the services and nodes of a Consul cluster, displays them live, and store them for later use.
This is useful for debugging an unstable service, figuring out why a service wasn't available yesterday at 2 AM or why a node failed from Consul's perpective.

## How to run

Consul Timeline is a single binary and takes a config file, or cli flag as consiguration.
Here are some config examples for common usages

### Basic

This will run the server on 0.0.0.0:8888 with memory storage, for demonstration purpose.

```yaml
consul:
  address: localhost:8500
  token: your_acl_token # Consul token, if needed.

server:
  listen: :8888
```


### Store events in MySQL

To view the MySQL shema needed by Consul Timeline, use :
```bash
consul-timeline --mysql-print-schema
```

To automatically setup the schema at startup, see config below.

```yaml
storage: mysql

consul:
  address: localhost:8500

server:
  listen: :8888

mysql:
  host: localhost
  port: 3306
  user: root
  password: root
  database: consul_timeline

  # While not necessary, purging is recommended to avoid the table growing indefinitely
  purge_frequency: 10000  # purge old events every N writes. set to 0 to disable
  purge_max_age_hours: 24

  setup_schema: true # Automatically setup the schema on startup
```


### Mutiple instances

For higher availability, multiple instances can be ran. In this case, use a Consul lock so that only one instance writes to the storage at a time

```yaml
consul:
  address: localhost:8500
  enable_distributed_lock: true
  lock_path: path/to/consul/timeline/lock # Path in Consul's KV to the lock, defaults to 'consul_timeline/lock'
  token: your_acl_token                   # If ACLs are enabled. The token needs 'session' write, as kv write to the path of the lock

server:
  listen: :8888
```

### Full config reference

Values are default

```yaml
log_level: info
storage: memory

server:
  listen: :8888

consul:
  address: localhost:8500
  enable_distributed_lock: false
  lock_path: consul_timeline/lock
  token: ""

memory:
  max_size: 10000

mysql:
  host: localhost
  port: 3306
  user: ""
  password: ""
  database: consul_timeline
  purge_frequency: 10000
  purge_max_age_hours: 336 # 2 weeks
  setup_schema: false

```

## Contributing

### To do

* Handle server errors in UI

* Metrics
