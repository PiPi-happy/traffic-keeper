# syntax=docker/dockerfile:1
# Multi-stage build for the traffic-keeper master server.
# CGO is disabled (modernc.org/sqlite is pure Go) so the binary is fully static.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
        -o /out/traffic-keeper-master ./cmd/master

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/traffic-keeper-master /usr/local/bin/traffic-keeper-master
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/traffic-keeper-master"]
