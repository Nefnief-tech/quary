# Build the panel binary.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/panel ./panel/cmd/panel

# Minimal runtime image, non-root. The data dir is pre-created so the named
# volume inherits quarry's ownership on first mount.
FROM alpine:3.20
RUN adduser -D -u 1500 quarry && apk add --no-cache ca-certificates \
	&& mkdir -p /var/lib/quarry && chown quarry:quarry /var/lib/quarry
USER quarry
COPY --from=build /out/panel /usr/local/bin/panel
EXPOSE 8080 9010
ENTRYPOINT ["panel"]
CMD ["serve"]
