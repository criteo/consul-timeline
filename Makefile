GOOS ?= linux
GOARCH ?= amd64
OUT ?= consul-timeline
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

release:
	env GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o $(OUT)

# Local bench: MariaDB, a Consul cluster fed by the load generator, and the
# app, see bench/README.md. A bench/.env (copied from bench/.env.example)
# points the app at a real cluster instead.
BENCH_ENV = $(wildcard bench/.env)
BENCH_COMPOSE = docker compose -f bench/compose.yaml $(if $(BENCH_ENV),--env-file $(BENCH_ENV),)
RATE ?= 15
FAMILIES ?= 40
PODS ?= 6

bench-up:
	RATE=$(RATE) FAMILIES=$(FAMILIES) PODS=$(PODS) $(BENCH_COMPOSE) up -d --build

bench-logs:
	$(BENCH_COMPOSE) logs -f --tail=50 timeline loadgen

bench-down:
	$(BENCH_COMPOSE) down -v

.PHONY: release bench-up bench-logs bench-down
