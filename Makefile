.PHONY: db-up db-down dev-api dev-web test-api-integration

TEST_DATABASE_URL ?= postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

dev-api:
	cd apps/api && CGO_ENABLED=0 go run ./cmd/server

dev-web:
	corepack pnpm@10.34.5 dev:web

test-api-integration: db-up
	cd apps/api && TEST_DATABASE_URL=$(TEST_DATABASE_URL) CGO_ENABLED=0 go test ./internal/store/postgres -count=1 -v
