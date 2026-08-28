# Multi-stage build producing one minimal image per binary (api/ingest/worker
# — see cmd/*/main.go for why they're separate processes). Build a specific
# one with `docker build --target api .` (docker-compose.yml does this).
#
# Build context is the REPO ROOT (not backend/) -- see docker-compose.yml --
# because the api image also needs db/migrations and db/init baked in for
# the external-database-migration Settings feature (internal/dbmigrate runs
# golang-migrate as a library plus hand-ported role/RLS setup against a
# customer-supplied target at runtime, so it needs the actual migration SQL
# on disk in its own container, not just reachable from the host).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
COPY db/ ./db/
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/ingest ./cmd/ingest
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/worker ./cmd/worker

FROM alpine:3.20 AS base
# tzdata: alpine ships with no /usr/share/zoneinfo at all, so
# time.LoadLocation fails for every IANA name except the hardcoded "UTC" --
# needed by all three binaries wherever a tenant's configured timezone
# (Settings -> On-Call Schedule, or any other non-UTC tenant timezone) gets
# resolved (OnCallScheduleService.ResolveCurrentAnalyst and friends).
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H kuruops
USER kuruops

FROM base AS api
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build --chown=kuruops /src/db/migrations /app/db/migrations
# DEV_KEYS_DIR's mount point (see docker-compose.yml's dev-jwt-keys volume)
# -- created here, owned by kuruops (the non-root USER above), so Docker
# copies that ownership into the named volume the first time it's mounted
# empty. Without this the volume mounts root-owned and the api process
# (running as kuruops, never root) can't write its dev JWT keypair to it.
RUN mkdir -p /app/.dev-keys
ENTRYPOINT ["/usr/local/bin/api"]

FROM base AS ingest
COPY --from=build /out/ingest /usr/local/bin/ingest
ENTRYPOINT ["/usr/local/bin/ingest"]

FROM base AS worker
COPY --from=build /out/worker /usr/local/bin/worker
ENTRYPOINT ["/usr/local/bin/worker"]
