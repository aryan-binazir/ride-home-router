# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY migrations ./migrations
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/ride-home-router ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
	&& adduser -D -H -u 10001 router
COPY --from=build /out/ride-home-router /usr/local/bin/ride-home-router
USER router
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s CMD wget -qO- "http://127.0.0.1:${PORT:-8080}/api/v1/health" || exit 1
# The server has no authentication: ALLOWED_HOSTS must name the public hostname
# served by the Cloudflare Tunnel (or proxy) in front of it, and the container
# must not be given a public domain of its own. Shell form expands $PORT; exec
# keeps the server as PID 1 so it receives SIGTERM for graceful shutdown.
CMD ["sh", "-c", "exec ride-home-router --addr 0.0.0.0:${PORT:-8080} --allowed-hosts \"${ALLOWED_HOSTS:?set ALLOWED_HOSTS to the public hostname(s)}\""]
