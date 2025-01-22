GOOS ?= linux
GOARCH ?= amd64
OUT ?= consul-timeline

deps:
	go install github.com/rakyll/statik

static: deps
	statik -f -src=./public -dest=server/ -p public

release: static
	env GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -tags release -o $(OUT)

.PHONY: static deps
