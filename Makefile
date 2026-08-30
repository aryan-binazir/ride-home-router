# Local development uses the Podman Postgres on port 5434 by default.

POSTGRES_CONTAINER ?= ride-home-router-postgres
POSTGRES_PORT ?= 5434
LOCAL_DATABASE_URL := postgres://postgres:postgres@localhost:$(POSTGRES_PORT)/ride_home_router?sslmode=disable
LOCAL_TEST_DATABASE_URL := postgres://postgres:postgres@localhost:$(POSTGRES_PORT)/ride_home_router_test?sslmode=disable
INHERITED_DATABASE_URL := $(if $(filter environment command line,$(origin DATABASE_URL)),$(DATABASE_URL))
DATABASE_URL ?= $(LOCAL_DATABASE_URL)
TEST_DATABASE_URL ?= $(LOCAL_TEST_DATABASE_URL)
export DATABASE_URL TEST_DATABASE_URL

.PHONY: help check check-unit lint verify vet test test-unit build serve clean postgres-up postgres-down psql migrate migrate-version migrate-down migrate-create

help:
	@echo "Ride Home Router"
	@echo ""
	@echo "  make serve         Migrate, then run the server on 127.0.0.1:$${PORT:-8080}"
	@echo "  make build         Build bin/ride-home-router and bin/migrate (CGO_ENABLED=0)"
	@echo "  make migrate       Apply pending migrations to DATABASE_URL"
	@echo "  make migrate-version Show DATABASE_URL migration version and dirty state"
	@echo "  make migrate-down CONFIRM=yes Roll back one migration on the fixed local database"
	@echo "  make migrate-create name=add_field Create paired timestamped SQL files"
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
	@rm -f bin/ride-home-router bin/migrate
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/ride-home-router ./cmd/server
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/migrate ./cmd/migrate

serve: migrate
	go run ./cmd/server

migrate:
	go run ./cmd/migrate

migrate-version:
	go run ./cmd/migrate version

migrate-down: override DATABASE_URL := $(LOCAL_DATABASE_URL)
migrate-down: export REQUESTED_DATABASE_URL := $(INHERITED_DATABASE_URL)
migrate-down:
	@if [ "$(CONFIRM)" != "yes" ]; then \
		echo "Refusing destructive target. Re-run with CONFIRM=yes"; \
		exit 1; \
	fi
	@if [ -n "$$REQUESTED_DATABASE_URL" ] && [ "$$REQUESTED_DATABASE_URL" != "$$DATABASE_URL" ]; then \
		echo "Refusing destructive target because inherited DATABASE_URL is not the fixed local database"; \
		exit 1; \
	fi
	go run ./cmd/migrate down --confirm

migrate-create: export MIGRATION_NAME := $(name)
migrate-create:
	@set -eu; \
	if [ -z "$$MIGRATION_NAME" ]; then \
		echo "name is required, e.g. make migrate-create name=add_field"; \
		exit 1; \
	fi; \
	version=$$(date -u +%Y%m%d%H%M%S); \
		slug=$$(printf '%s' "$$MIGRATION_NAME" | tr '[:upper:] -' '[:lower:]__' | sed 's/[^a-z0-9_]/_/g; s/__*/_/g; s/^_//; s/_$$//'); \
		if [ -z "$$slug" ]; then echo "name must contain a letter or number"; exit 1; fi; \
		up="migrations/$${version}_$${slug}.up.sql"; \
		down="migrations/$${version}_$${slug}.down.sql"; \
		lock="migrations/.$${version}.lock"; \
		if ! mkdir "$$lock"; then echo "migration creation already owns timestamp $$version"; exit 1; fi; \
		cleanup() { \
			status=$$?; \
			trap - 0 1 2 15; \
			if [ "$$status" -ne 0 ]; then \
				if [ -e "$$up" ] && [ "$$up" -ef "$$lock/up.owner" ]; then rm -f "$$up"; fi; \
				if [ -e "$$down" ] && [ "$$down" -ef "$$lock/down.owner" ]; then rm -f "$$down"; fi; \
			fi; \
			rm -f "$$lock/up.sql" "$$lock/down.sql" "$$lock/up.owner" "$$lock/down.owner"; \
			if ! rmdir "$$lock"; then status=1; fi; \
			exit "$$status"; \
		}; \
		trap cleanup 0; \
		trap 'exit 1' 1 2 15; \
		set -- migrations/$${version}_*.sql; \
		if [ -e "$$1" ]; then echo "migration already exists for timestamp $$version"; exit 1; fi; \
		printf '%s\n' '-- Write migration here.' > "$$lock/up.sql"; \
		printf '%s\n' '-- ride-home-router: down migration disabled' > "$$lock/down.sql"; \
		ln "$$lock/up.sql" "$$lock/up.owner"; \
		ln "$$lock/down.sql" "$$lock/down.owner"; \
		mv "$$lock/up.sql" "$$up"; \
		mv "$$lock/down.sql" "$$down"; \
		printf '%s\n%s\n' "$$up" "$$down"

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
