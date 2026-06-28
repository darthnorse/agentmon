.PHONY: test build build-web build-hub build-agent embed embed-agents docker clean

test:
	go test ./shared/... ./agent/... ./hubd/...

build-web:
	cd web && npm ci && npm run build

# Copy the built SPA where //go:embed expects it (overwrites the placeholder).
embed: build-web
	rm -rf hubd/internal/webui/dist
	cp -r web/dist hubd/internal/webui/dist

# Cross-compile both agent arches where //go:embed expects them (overwrites the placeholders).
embed-agents:
	rm -f hubd/internal/agentbin/bin/agent-linux-amd64 hubd/internal/agentbin/bin/agent-linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
		-o hubd/internal/agentbin/bin/agent-linux-amd64 ./agent/cmd/agentmon-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
		-o hubd/internal/agentbin/bin/agent-linux-arm64 ./agent/cmd/agentmon-agent

build-hub: embed embed-agents
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/agentmon-hubd ./hubd/cmd/agentmon-hubd
	rm -rf hubd/internal/webui/dist
	git checkout -- hubd/internal/webui/dist/index.html \
		hubd/internal/agentbin/bin/agent-linux-amd64 hubd/internal/agentbin/bin/agent-linux-arm64

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o bin/agentmon-agent ./agent/cmd/agentmon-agent

build: build-hub build-agent

docker:
	docker build -f deploy/Dockerfile -t agentmon-hubd:dev .

clean:
	rm -rf bin web/dist hubd/internal/webui/dist
	git checkout -- hubd/internal/webui/dist/index.html \
		hubd/internal/agentbin/bin/agent-linux-amd64 hubd/internal/agentbin/bin/agent-linux-arm64
