.PHONY: all build web-build build-proxy test test-race lint web-dev dev-proxy clean

BINARY_NAME=freebuff-proxy
BIN_DIR=bin

all: build

web-build:
	npm --prefix frontend run build

build-proxy:
	go build -tags dashboard -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/freebuff-proxy

build: web-build build-proxy

test:
	env -u AUTH_TOKENS -u ADMIN_TOKEN go test ./...

test-race:
	env -u AUTH_TOKENS -u ADMIN_TOKEN go test -race ./...

lint:
	go vet ./...
	golangci-lint run ./...

web-dev:
	npm --prefix frontend run dev

dev-proxy:
	go run -tags dashboard ./cmd/freebuff-proxy

clean:
	rm -rf $(BIN_DIR)
