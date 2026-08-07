# Multi-stage build: compile the Vite app, then serve the static output
# through nginx, which also reverse-proxies /api and /auth to the api
# service on the Docker network. This mirrors exactly what vite.config.ts's
# dev-server proxy does -- the browser only ever talks to one origin, so
# there's no CORS story to build on the Go side just for this.
FROM node:20-alpine AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
