# Build the daemon binary.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/quarry-daemon ./daemon/cmd/daemon

# Minimal runtime image, non-root. The Docker socket is mounted (read-only)
# at runtime for container management (Phase 1).
FROM alpine:3.20
RUN adduser -D -u 1500 quarry && apk add --no-cache ca-certificates docker-cli
USER quarry
COPY --from=build /out/quarry-daemon /usr/local/bin/quarry-daemon
ENTRYPOINT ["quarry-daemon"]
