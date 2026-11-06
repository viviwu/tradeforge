GO ?= go
BUF ?= buf

.PHONY: help proto proto-lint proto-generate build test test-integration fmt vet tidy migrate-up migrate-down compose-up compose-down clean

help:
	@printf '%s\n' \
		'make proto          Lint and generate protobuf code' \
		'make build          Build all Go packages' \
		'make test           Run unit tests' \
		'make test-integration Run PostgreSQL integration tests' \
		'make fmt            Format Go sources' \
		'make vet            Run go vet' \
		'make tidy           Tidy Go modules' \
		'make migrate-up     Apply database migrations' \
		'make migrate-down   Roll back one migration' \
		'make compose-up     Start local dependencies' \
		'make compose-down   Stop local dependencies'

proto: proto-lint proto-generate

proto-lint:
	$(BUF) lint

proto-generate:
	$(BUF) generate

build:
	$(GO) build ./...

test:
	$(GO) test ./...

test-integration:
	TRADEFORGE_TEST_DSN="$${TRADEFORGE_TEST_DSN:-postgres://tradeforge:tradeforge@localhost:55432/tradeforge?sslmode=disable}" \
		$(GO) test -tags=integration ./internal/oms -v

fmt:
	gofmt -w $$($(GO) list -f '{{.Dir}}' ./...)

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

migrate-up:
	migrate -path migrations -database "$$TRADEFORGE_DB_DSN" up

migrate-down:
	migrate -path migrations -database "$$TRADEFORGE_DB_DSN" down 1

compose-up:
	docker compose -f deploy/docker-compose.yml up -d

compose-down:
	docker compose -f deploy/docker-compose.yml down

clean:
	rm -rf build coverage.out coverage.html
