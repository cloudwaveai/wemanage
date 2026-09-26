.PHONY: test test-db-up test-db-down build vet tidy web

TEST_PG_CONTAINER ?= aiseat-testpg
TEST_PG_PORT ?= 55432

test-db-up:
	@docker ps -a --format '{{.Names}}' | grep -q '^$(TEST_PG_CONTAINER)$$' || \
		docker run -d --name $(TEST_PG_CONTAINER) \
			-e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=postgres \
			-p $(TEST_PG_PORT):5432 postgres:16-alpine >/dev/null
	@echo "waiting for postgres..."
	@for i in $$(seq 1 30); do docker exec $(TEST_PG_CONTAINER) pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
	@echo "test db ready on :$(TEST_PG_PORT)"

test-db-down:
	@docker rm -f $(TEST_PG_CONTAINER) >/dev/null 2>&1 || true

test:
	GOPROXY=$${GOPROXY:-https://goproxy.cn,direct} go test -race ./...

vet:
	go vet ./...

tidy:
	GOPROXY=$${GOPROXY:-https://goproxy.cn,direct} go mod tidy

build:
	CGO_ENABLED=0 go build -o gateway ./cmd/gateway

web:
	cd web && pnpm install && pnpm build
