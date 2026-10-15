# Multi-stage build producing one minimal image per binary (api/ingest/worker/analyzer
# — see cmd/*/main.go for why they're separate processes). Build a specific
# one with `docker build --target api .` (docker-compose.yml does this).
#
# Build context is the REPO ROOT (not backend/) -- see docker-compose.yml --
# because the api image also needs db/migrations and db/init baked in for
# the external-database-migration Settings feature (internal/dbmigrate runs
# golang-migrate as a library plus hand-ported role/RLS setup against a
# customer-supplied target at runtime, so it needs the actual migration SQL
# on disk in its own container, not just reachable from the host).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
COPY db/ ./db/
# One build id for all three binaries: a hash of every source file that goes into
# them, computed BEFORE compiling so the three images built from the same tree
# agree on it. Each binary logs it at startup and exports it as
# kuruops_build_info, so a partial deploy (only api rebuilt, ingest/worker still
# on older code) is visible instead of silent -- see internal/buildinfo.
RUN find . -type f \( -name '*.go' -o -name '*.sql' -o -name go.mod -o -name go.sum \) | sort | xargs sha256sum | sha256sum | cut -c1-12 > /build_id
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/kuruops/kuruops/internal/buildinfo.Build=$(cat /build_id)" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/kuruops/kuruops/internal/buildinfo.Build=$(cat /build_id)" -o /out/ingest ./cmd/ingest
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/kuruops/kuruops/internal/buildinfo.Build=$(cat /build_id)" -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/kuruops/kuruops/internal/buildinfo.Build=$(cat /build_id)" -o /out/analyzer ./cmd/analyzer

FROM alpine:3.24 AS base
# tzdata: alpine ships with no /usr/share/zoneinfo at all, so
# time.LoadLocation fails for every IANA name except the hardcoded "UTC" --
# needed by all three binaries wherever a tenant's configured timezone
# (Settings -> On-Call Schedule, or any other non-UTC tenant timezone) gets
# resolved (OnCallScheduleService.ResolveCurrentAnalyst and friends).
# apk upgrade before installing: a base image tag is rebuilt on its own
# schedule, so between an OS package CVE being fixed upstream and the tag
# being republished, every build of this image ships the vulnerable version.
# Upgrading here closes that window -- the container scan flagged
# libcrypto3 3.5.7-r0 (CVE-2026-14456, an OpenSSL denial of service) while
# alpine:3.24 still carried it.
RUN apk --no-cache upgrade && apk add --no-cache ca-certificates tzdata && adduser -D -H -u 1000 kuruops
# Numeric, not "USER kuruops": a pod with runAsNonRoot: true is refused ("image has
# non-numeric user, cannot verify user is non-root") when the image's user is a name,
# so by name none of the Kubernetes Deployments could start. 1000 is also what
# 04-api.yaml's fsGroup assumes.
USER 1000:1000

FROM base AS api
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build --chown=kuruops /src/db/migrations /app/db/migrations
# DEV_KEYS_DIR's mount point (see docker-compose.yml's dev-jwt-keys volume)
# -- created here, owned by kuruops (the non-root USER above), so Docker
# copies that ownership into the named volume the first time it's mounted
# empty. Without this the volume mounts root-owned and the api process
# (running as kuruops, never root) can't write its dev JWT keypair to it.
RUN mkdir -p /app/.dev-keys
# UPLOAD_DIR's mount point (the "uploads" volume), same reasoning: a named volume
# mounted empty inherits the ownership of this directory, and without it the volume
# came up root-owned so the kuruops user could not store a single attachment
# ("could not store uploaded file"). Needs root only for the chown.
USER root
RUN mkdir -p /data/uploads && chown kuruops:kuruops /data/uploads
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/api"]

FROM base AS ingest
COPY --from=build /out/ingest /usr/local/bin/ingest
ENTRYPOINT ["/usr/local/bin/ingest"]

FROM base AS worker
COPY --from=build /out/worker /usr/local/bin/worker
ENTRYPOINT ["/usr/local/bin/worker"]

FROM base AS analyzer
COPY --from=build /out/analyzer /usr/local/bin/analyzer
ENTRYPOINT ["/usr/local/bin/analyzer"]
