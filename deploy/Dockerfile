# syntax=docker/dockerfile:1
#
# Three stages: build the frontend, build the master (embedding the frontend),
# then a small runtime image. The agent is distributed via GitHub Releases, not
# this image.

# 1. frontend
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# 2. master (embeds the freshly built frontend)
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# overlay the real built assets onto the embedded location
COPY --from=web /web/dist /src/internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
        -o /out/traffic-keeper-master ./cmd/master

# 3. runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/traffic-keeper-master /usr/local/bin/traffic-keeper-master
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/traffic-keeper-master"]
