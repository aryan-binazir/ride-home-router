# Ride Home Router — server build and verification targets.
# `make serve` runs the server on 127.0.0.1:$PORT (default 8080).

.PHONY: help check lint verify vet test build serve clean

help:
	@echo "Ride Home Router"
	@echo ""
	@echo "  make serve    Run the server locally (127.0.0.1:$${PORT:-8080})"
	@echo "  make build    Build bin/ride-home-router (CGO_ENABLED=0)"
	@echo "  make check    lint + verify + vet + test"
	@echo "  make lint     golangci-lint"
	@echo "  make verify   go mod tidy -diff && go mod verify"
	@echo "  make test     Go and JS tests"
	@echo "  make clean    Remove build artifacts"

check: lint verify vet test

lint:
	golangci-lint run ./...

verify:
	go mod tidy -diff
	go mod verify

vet:
	go vet ./...

test:
	node --test web/static/js/*.test.js
	go test -race -count=1 -coverprofile=coverage.out ./...

build:
	@mkdir -p bin
	@rm -f bin/ride-home-router
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/ride-home-router ./cmd/server

serve:
	go run ./cmd/server

clean:
	rm -rf bin coverage.out
