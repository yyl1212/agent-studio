.PHONY: db-up db-down dev-api dev-web

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

dev-api:
	cd apps/api && CGO_ENABLED=0 go run ./cmd/server

dev-web:
	corepack pnpm@10.34.5 dev:web
