.PHONY: db-up db-down dev-api dev-web generate check-generated test-api-integration verify test-e2e test-sdk-e2e

TEST_DATABASE_URL ?= postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

dev-api:
	set -a; [ ! -f .env ] || . ./.env; set +a; CGO_ENABLED=0 go run ./apps/api/cmd/server

dev-web:
	corepack pnpm@10.34.5 dev:web

generate:
	CGO_ENABLED=0 go run ./cmd/agent-studio generate

check-generated: generate
	git diff --exit-code -- apps/api/internal/generated/nodes_gen.go

test-api-integration: db-up
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) CGO_ENABLED=0 go test ./apps/api/internal/store/postgres -count=1 -v

verify: db-up check-generated
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) CGO_ENABLED=0 go test ./... -count=1
	CGO_ENABLED=0 go vet ./...
	corepack pnpm@10.34.5 --filter @agent-studio/web generate:api
	git diff --exit-code -- apps/web/src/lib/api/generated.ts
	corepack pnpm@10.34.5 lint
	corepack pnpm@10.34.5 test
	corepack pnpm@10.34.5 build

test-e2e: db-up
	corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test

test-sdk-e2e: db-up
	CGO_ENABLED=0 go test ./internal/generatedtest -count=1 -v
	corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/sdk-node.spec.ts
