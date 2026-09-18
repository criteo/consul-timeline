# Builds the UI, the app and the bench tools from this checkout. Used by
# bench/compose.yaml.
FROM node:20-alpine AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
RUN apk add --no-cache make git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/public/dist/ ./public/dist/
RUN make release OUT=/out/consul-timeline GOARCH=$(go env GOARCH) \
 && CGO_ENABLED=0 go build -o /out/bench-loadgen ./bench/loadgen

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
COPY --from=build /out/ /usr/local/bin/
EXPOSE 8888
CMD ["consul-timeline"]
