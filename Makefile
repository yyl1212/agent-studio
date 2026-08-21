.PHONY: db-up db-down dev-api dev-web generate check-generated test-api-integration verify verify-go-quick verify-web-quick verify-quick verify-node-index test-e2e test-sdk-e2e release-tools release-check release-snapshot release-preflight verify-workflows verify-release

TEST_DATABASE_URL ?= postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable
RELEASE_TOOLS_DIR ?= $(CURDIR)/.release-tools/bin
GORELEASER ?= $(RELEASE_TOOLS_DIR)/goreleaser
SYFT ?= $(RELEASE_TOOLS_DIR)/syft
ACTIONLINT_VERSION ?= v1.7.12

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

verify-go-quick: check-generated
	CGO_ENABLED=0 go test ./... -count=1
	CGO_ENABLED=0 go vet ./...
	sh scripts/check-version_test.sh
	sh scripts/check-release-artifacts_test.sh

verify-web-quick:
	corepack pnpm@10.34.5 --filter @agent-studio/web generate:api
	git diff --exit-code -- apps/web/src/lib/api/generated.ts
	corepack pnpm@10.34.5 lint
	corepack pnpm@10.34.5 typecheck
	corepack pnpm@10.34.5 test
	corepack pnpm@10.34.5 build

verify-quick: verify-go-quick verify-web-quick

verify-node-index:
	CGO_ENABLED=0 go test ./internal/nodeindex ./internal/cli -count=1
	shasum -a 256 -c contracts/node-index-source.checksums

test-e2e: db-up
	corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test

test-sdk-e2e: db-up
	CGO_ENABLED=0 go test ./internal/generatedtest -count=1 -v
	corepack pnpm@10.34.5 --filter @agent-studio/web exec playwright test e2e/sdk-node.spec.ts

release-tools:
	RELEASE_TOOLS_DIR=$(RELEASE_TOOLS_DIR) sh scripts/install-release-tools.sh

release-check:
	command -v "$(GORELEASER)" >/dev/null
	command -v "$(SYFT)" >/dev/null
	PATH="$(dir $(SYFT)):$$PATH" "$(GORELEASER)" check

release-snapshot:
	command -v "$(GORELEASER)" >/dev/null
	command -v "$(SYFT)" >/dev/null
	PATH="$(dir $(SYFT)):$$PATH" "$(GORELEASER)" release --clean --snapshot --skip=publish
	sh scripts/check-release-artifacts.sh collection dist "v0.3.1-snapshot"

release-preflight:
	@test -n "$(TAG)" || { printf '%s\n' 'usage: make release-preflight TAG=vX.Y.Z[-rc.N]' >&2; exit 2; }
	sh scripts/check-version.sh "$(TAG)"
	bash scripts/check-release-immutability.sh preflight

verify-workflows:
	sh scripts/check-release-immutability_test.sh
	sh scripts/check-release-workflow_test.sh
	CGO_ENABLED=0 go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

verify-release: release-tools
	$(MAKE) release-check
	$(MAKE) release-snapshot
	$(MAKE) verify-workflows
