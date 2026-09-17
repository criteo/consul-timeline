# Builds the app and the bench tools from this checkout. Used by bench/compose.yaml.
FROM golang:1.25-alpine AS build
RUN apk add --no-cache make git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make release OUT=/out/consul-timeline GOARCH=$(go env GOARCH) \
 && CGO_ENABLED=0 go build -o /out/bench-loadgen ./bench/loadgen \
 && CGO_ENABLED=0 go build -o /out/bench-import ./bench/import

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
COPY --from=build /out/ /usr/local/bin/
EXPOSE 8888
CMD ["consul-timeline"]
