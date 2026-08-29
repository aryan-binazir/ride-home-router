# Local development uses the Podman Postgres on port 5434 by default.

POSTGRES_CONTAINER ?= ride-home-router-postgres
POSTGRES_PORT ?= 5434
LOCAL_DATABASE_URL := postgres://postgres:postgres@localhost:$(POSTGRES_PORT)/ride_home_router?sslmode=disable
LOCAL_TEST_DATABASE_URL := postgres://postgres:postgres@localhost:$(POSTGRES_PORT)/ride_home_router_test?sslmode=disable
DATABASE_URL ?= $(LOCAL_DATABASE_URL)
TEST_DATABASE_URL ?= $(LOCAL_TEST_DATABASE_URL)
export DATABASE_URL TEST_DATABASE_URL

.PHONY: help check check-unit lint verify vet test test-unit build serve clean postgres-up postgres-down psql

help:
	@echo "Ride Home Router"
	@echo ""
	@echo "  make serve         Run the server on 127.0.0.1:$${PORT:-8080} against DATABASE_URL"
	@echo "  make build         Build bin/ride-home-router (CGO_ENABLED=0)"
	@echo "  make check         lint + verify + vet + all tests (needs TEST_DATABASE_URL)"
	@echo "  make check-unit    Same without database-backed tests"
	@echo "  make postgres-up   Start the local podman Postgres (port $(POSTGRES_PORT))"
	@echo "  make postgres-down Remove the local podman Postgres"
	@echo "  make psql          Open psql on the local Postgres"
	@echo "  make clean         Remove build artifacts"

check: lint verify vet test

check-unit: lint verify vet test-unit

lint:
	golangci-lint run ./...

verify:
	go mod tidy -diff
	go mod verify

vet:
	go vet ./...

# test-unit clears TEST_DATABASE_URL to skip database tests.
test:
	node --test web/static/js/*.test.js
	go test -race -count=1 -coverprofile=coverage.out ./...

test-unit:
	node --test web/static/js/*.test.js
	TEST_DATABASE_URL= go test -race -count=1 ./...

build:
	@mkdir -p bin
	@rm -f bin/ride-home-router
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/ride-home-router ./cmd/server

serve:
	go run ./cmd/server

clean:
	rm -rf bin coverage.out

postgres-up:
	podman run -d --name $(POSTGRES_CONTAINER) -p $(POSTGRES_PORT):5432 \
		-e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ride_home_router \
		docker.io/library/postgres:18
	@for i in $$(seq 1 30); do podman exec $(POSTGRES_CONTAINER) pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
	podman exec $(POSTGRES_CONTAINER) psql -U postgres -c 'CREATE DATABASE ride_home_router_test'

postgres-down:
	podman rm -f $(POSTGRES_CONTAINER)

psql:
	podman exec -it $(POSTGRES_CONTAINER) psql -U postgres -d ride_home_router
