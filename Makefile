.PHONY: db-up db-down observability-up observability-down observability-check observability-verify dev-api dev-worker dev-web dev-stack generate check-generated test-api-integration verify verify-go-quick verify-web-quick verify-quick verify-node-index test-e2e test-sdk-e2e test-durable-runs-e2e backup-create backup-inspect backup-restore-dry-run backup-restore test-backup-e2e verify-backup-docs verify-backup-fixture release-tools release-check release-snapshot release-preflight verify-workflows verify-release

TEST_DATABASE_URL ?= postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable
override TEST_DATABASE_URL := $(value TEST_DATABASE_URL)
EXTERNAL_DB ?= 0
RELEASE_TOOLS_DIR ?= $(CURDIR)/.release-tools/bin
GORELEASER ?= $(RELEASE_TOOLS_DIR)/goreleaser
SYFT ?= $(RELEASE_TOOLS_DIR)/syft
ACTIONLINT_VERSION ?= v1.7.12

export OUTPUT
export BACKUP
export CONFIRM

test-api-integration verify backup-create backup-restore-dry-run backup-restore test-backup-e2e: export TEST_DATABASE_URL := $(value TEST_DATABASE_URL)

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

observability-up:
	docker compose --profile observability up -d otel-collector prometheus jaeger
	sh scripts/wait-observability.sh

observability-down:
	docker compose --profile observability stop otel-collector prometheus jaeger

observability-check:
	sh scripts/check-observability-compose.sh

observability-verify:
	sh scripts/verify-observability.sh

dev-api:
	set -a; [ ! -f .env ] || . ./.env; set +a; CGO_ENABLED=0 go run ./apps/api/cmd/server

dev-worker:
	set -a; [ ! -f .env ] || . ./.env; set +a; CGO_ENABLED=0 go run ./apps/api/cmd/worker

dev-web:
	corepack pnpm@10.34.5 dev:web

dev-stack:
	set -a; [ ! -f .env ] || . ./.env; set +a; docker compose up --build db api worker

generate:
	CGO_ENABLED=0 go run ./cmd/agent-studio generate

check-generated: generate
	git diff --exit-code -- apps/api/internal/generated/nodes_gen.go

test-api-integration: db-up
	@TEST_DATABASE_URL="$$TEST_DATABASE_URL" CGO_ENABLED=0 go test ./apps/api/internal/store/postgres -count=1 -v

verify: db-up check-generated
	@TEST_DATABASE_URL="$$TEST_DATABASE_URL" CGO_ENABLED=0 go test -p 1 ./... -count=1
	CGO_ENABLED=0 go vet ./...
	corepack pnpm@10.34.5 --filter @agent-studio/web generate:api
	git diff --exit-code -- apps/web/src/lib/api/generated.ts
	corepack pnpm@10.34.5 lint
	corepack pnpm@10.34.5 test
	corepack pnpm@10.34.5 build

verify-go-quick: check-generated verify-backup-docs verify-backup-fixture
	CGO_ENABLED=0 go test -p 1 ./... -count=1
	CGO_ENABLED=0 go vet ./...
	sh scripts/check-version_test.sh
	sh scripts/check-release-artifacts_test.sh

verify-backup-docs:
	sh scripts/check-backup-docs_test.sh

verify-backup-fixture:
	CGO_ENABLED=0 go run ./internal/backup/testdata/generate --check

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

test-durable-runs-e2e:
	sh scripts/test-durable-runs-e2e.sh

backup-create: db-up
	@test -n "$$OUTPUT" || { printf '%s\n' 'usage: make backup-create OUTPUT=/path/file.asbak' >&2; exit 2; }
	@DATABASE_URL="$$TEST_DATABASE_URL" CGO_ENABLED=0 go run ./cmd/agent-studio backup create --output "$$OUTPUT"

backup-inspect:
	@test -n "$$BACKUP" || { printf '%s\n' 'usage: make backup-inspect BACKUP=/path/file.asbak' >&2; exit 2; }
	CGO_ENABLED=0 go run ./cmd/agent-studio backup inspect "$$BACKUP"

backup-restore-dry-run: db-up
	@test -n "$$BACKUP" || { printf '%s\n' 'usage: make backup-restore-dry-run BACKUP=/path/file.asbak' >&2; exit 2; }
	@DATABASE_URL="$$TEST_DATABASE_URL" CGO_ENABLED=0 go run ./cmd/agent-studio backup restore --dry-run "$$BACKUP"

backup-restore: db-up
	@test "$$CONFIRM" = "empty-instance" || { printf '%s\n' 'set CONFIRM=empty-instance' >&2; exit 2; }
	@test -n "$$BACKUP" || { printf '%s\n' 'usage: make backup-restore BACKUP=/path/file.asbak CONFIRM=empty-instance' >&2; exit 2; }
	@DATABASE_URL="$$TEST_DATABASE_URL" CGO_ENABLED=0 go run ./cmd/agent-studio backup restore --confirm-empty-instance "$$BACKUP"

ifeq ($(EXTERNAL_DB),1)
test-backup-e2e:
else
test-backup-e2e: db-up
endif
	@TEST_DATABASE_URL="$$TEST_DATABASE_URL" sh scripts/test-backup-e2e.sh

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
	sh scripts/check-release-artifacts.sh collection dist "v0.4.0-snapshot"

release-preflight:
	@test -n "$(TAG)" || { printf '%s\n' 'usage: make release-preflight TAG=vX.Y.Z[-rc.N]' >&2; exit 2; }
	sh scripts/check-version.sh "$(TAG)"
	bash scripts/release-preflight.sh "$(TAG)"

verify-workflows:
	sh scripts/check-release-immutability_test.sh
	sh scripts/release-preflight_test.sh
	sh scripts/check-release-version_test.sh
	sh scripts/check-release-workflow_test.sh
	sh scripts/check-backup-workflow_test.sh
	CGO_ENABLED=0 go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

verify-release: release-tools
	$(MAKE) release-check
	$(MAKE) release-snapshot
	$(MAKE) verify-workflows
