# Meizon Framework Registry — build & test

GO      ?= go
BIN     ?= bin
PKGS    := ./...

# Point at a running Postgres to include the database-backed tests.
# e.g. make test REGISTRYD_TEST_PG_ADDR=localhost:5432
REGISTRYD_TEST_PG_ADDR ?=

.PHONY: all build test test-unit test-db vet fmt tidy clean pg-up pg-down

all: build

build:
	$(GO) build -o $(BIN)/registryd ./cmd/registryd
	$(GO) build -o $(BIN)/registryd-bootstrap ./cmd/registryd-bootstrap
	$(GO) build -o $(BIN)/registryctl ./cmd/registryctl

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -l -w pkg cmd

tidy:
	$(GO) mod tidy

# Pure-Go tests (no database needed): trust anchor + RBAC/region isolation.
test-unit:
	$(GO) test ./pkg/fwschema/... ./pkg/iam/... ./pkg/gid/...

# Full suite, including coredata + lifecycle e2e when a test DB is configured.
test:
	REGISTRYD_TEST_PG_ADDR=$(REGISTRYD_TEST_PG_ADDR) $(GO) test $(PKGS)

# Convenience: a disposable Postgres for the database-backed tests.
#
# Creates registryd_test alongside registryd. The database-backed harnesses
# truncate, so they refuse any database whose name does not end in _test — a
# guard added after a test run wiped a working registryd database. Keep the two
# separate here so `make test` never has the development data in reach.
pg-up:
	docker run -d --name meizon-registry-pg \
		-e POSTGRES_USER=registryd -e POSTGRES_PASSWORD=registryd -e POSTGRES_DB=registryd \
		-p 55432:5432 postgres:15
	@echo "waiting for postgres…"
	@until docker exec meizon-registry-pg pg_isready -U registryd -q 2>/dev/null; do sleep 1; done
	docker exec meizon-registry-pg createdb -U registryd registryd_test

pg-down:
	docker rm -f meizon-registry-pg

clean:
	rm -rf $(BIN)
