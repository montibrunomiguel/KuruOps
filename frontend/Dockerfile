# Multi-stage build: compile the Vite app, then serve the static output
# through nginx, which also reverse-proxies /api and /auth to the api
# service on the Docker network. This mirrors exactly what vite.config.ts's
# dev-server proxy does -- the browser only ever talks to one origin, so
# there's no CORS story to build on the Go side just for this.
#
# Build context is the repo root (see docker-compose.yml), not frontend/ --
# lib/onCallRotation.test.ts imports docs/oncall-rotation-fixtures.json as
# its cross-language source of truth (see that file's own doc comment), and
# `tsc`'s src-wide typecheck in `npm run build` fails if it can't resolve
# that import, so docs/ has to be reachable from inside the build.
FROM node:26-alpine AS build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/. .
COPY docs/oncall-rotation-fixtures.json /docs/oncall-rotation-fixtures.json
RUN npm run build

# nginx-unprivileged, not plain nginx:1.27-alpine: runs as a non-root user
# (uid 101) out of the box -- cache/pid dirs are already owned by it and it
# listens on 8080 (>1024, unprivileged ports only) instead of 80, both
# handled by the image itself with no nginx.conf changes beyond the listen
# port below. Same tag suffix as the image we're replacing.
FROM nginxinc/nginx-unprivileged:1.31-alpine
# The base image's own Alpine packages (openssl, c-ares, libexpat, libpng,
# ...) lag behind Alpine's repos between nginx image releases -- picked up
# by the new container-scan CI job (see .github/workflows/security.yml).
# Upgrading in place (not bumping the nginx version) keeps this a pure
# patch-level security fix.
USER root
RUN apk update && apk upgrade --no-cache
USER nginx
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 8080
