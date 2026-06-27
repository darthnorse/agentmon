.PHONY: test build build-web build-hub build-agent embed docker clean

test:
	go test ./shared/... ./agent/... ./hubd/...

build-web:
	cd web && npm ci && npm run build

# Copy the built SPA where //go:embed expects it (overwrites the placeholder).
embed: build-web
	rm -rf hubd/internal/webui/dist
	cp -r web/dist hubd/internal/webui/dist

build-hub: embed
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/agentmon-hubd ./hubd/cmd/agentmon-hubd

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/agentmon-agent ./agent/cmd/agentmon-agent

build: build-hub build-agent

docker:
	docker build -f deploy/Dockerfile -t agentmon-hubd:dev .

clean:
	rm -rf bin web/dist hubd/internal/webui/dist
	git checkout -- hubd/internal/webui/dist/index.html
