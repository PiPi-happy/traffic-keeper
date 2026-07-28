.PHONY: web build-master build-agent build test vet fmt clean

# Build the frontend and stage it where the master embeds it.
web:
	cd web && npm install && npm run build
	rm -rf internal/web/dist/assets
	cp -r web/dist/. internal/web/dist/

build-master: web
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/traffic-keeper-master ./cmd/master

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/traffic-keeper-agent ./cmd/agent

build: build-master build-agent

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf bin web/dist internal/web/dist/assets
