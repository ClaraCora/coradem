VERSION ?= $(shell tr -d '\r\n' < VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: build clean test install build-linux build-linux-arm64 build-all

# Build for current platform
build:
	go build -ldflags "$(LDFLAGS)" -tags "with_quic with_utls with_wireguard with_clash_api" -o corade ./cmd/corade
	go build -ldflags "$(LDFLAGS)" -o coradectl ./cmd/xbctl

# Build for Linux amd64
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -tags "with_quic with_utls with_wireguard with_acme with_clash_api" -o corade-linux-amd64 ./cmd/corade
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o coradectl-linux-amd64 ./cmd/xbctl

# Build for Linux arm64
build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -tags "with_quic with_utls with_wireguard with_acme with_clash_api" -o corade-linux-arm64 ./cmd/corade
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o coradectl-linux-arm64 ./cmd/xbctl

# Build all platforms
build-all: build-linux build-linux-arm64

# Run tests
test:
	go test -v -race -count=1 ./internal/...

# Clean build artifacts
clean:
	rm -f corade coradectl corade-linux-* coradectl-linux-*

# Install to system (single node, legacy compat)
install: build
	sudo cp corade /usr/local/bin/
	sudo cp coradectl /usr/local/bin/
	sudo mkdir -p /etc/corade
	@if [ ! -f /etc/corade/config.yml ]; then \
		sudo cp config.yml.example /etc/corade/config.yml; \
		echo "Config copied to /etc/corade/config.yml - please edit it"; \
	fi
