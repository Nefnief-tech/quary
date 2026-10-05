# Build the panel binary.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/panel ./panel/cmd/panel

# Minimal runtime image, non-root.
FROM alpine:3.20
RUN adduser -D -u 1500 quarry && apk add --no-cache ca-certificates
USER quarry
COPY --from=build /out/panel /usr/local/bin/panel
EXPOSE 8080 9010
ENTRYPOINT ["panel"]
CMD ["serve"]
