.PHONY: build-server build-agent-linux build-all clean web

# Build server for current platform
build-server:
	go build -o jcmanager-server ./cmd/server/

# Build agent binaries for Linux (cross-compile)
build-agent-linux:
	@mkdir -p agents
	GOOS=linux GOARCH=amd64 go build -o agents/jcmanager-agent-linux-amd64 ./cmd/agent/
	GOOS=linux GOARCH=arm64 go build -o agents/jcmanager-agent-linux-arm64 ./cmd/agent/

# Build frontend
web:
	cd web && npm install && npm run build

# Build everything
build-all: web build-server build-agent-linux

clean:
	rm -f jcmanager-server
	rm -rf agents/
