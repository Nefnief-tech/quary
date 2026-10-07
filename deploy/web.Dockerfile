# Build the web UI and serve it with Caddy: static SPA plus reverse proxy
# for the API and console WebSocket.
FROM node:22-alpine AS build
WORKDIR /src
RUN npm install -g pnpm@11
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM caddy:2-alpine
COPY deploy/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/build /srv
EXPOSE 80
