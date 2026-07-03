.PHONY: format test test-integration web-deps typecheck openapi openapi-check sqlc migrate helm-lint helm-template helm-template-fixtures helm-test helm-check helm-package check up down status bootstrap seed seed-offline seed-cache urls postgres nexus-password e2e compose-e2e e2e-live-cleanup docker-build compose-config fixture-wait fixture-capture-password fixture-accept-eula fixture-enable-docker-realm fixture-create-repos fixture-seed fixture-index fixture-seed-medium fixture-seed-large fixture-mutate fixture-probe

DB_URL ?= postgres://nexus_lens:nexus_lens@localhost:5432/nexus_lens?sslmode=disable
GO_TEST ?= env -u GOROOT go test
WEB_NODE_MODULES := web/node_modules/.package-lock.json
COMPOSE_FILE ?= deploy/compose/compose.yaml
WEB_DEV_HOST ?= localhost
WEB_DEV_PORT ?= 5173
WEB_DEV_PID ?= /tmp/nexus-lens-web-dev.pid
WEB_DEV_LOG ?= /tmp/nexus-lens-web-dev.log
SEED_SCALE ?= small
SEED_IMAGE_COUNT ?=
SEED_TAGS_PER_IMAGE ?=
SEED_COMMON ?= true
SEED_COMPLEX ?= true
SEED_COMPLEX_TAGS ?= 96
SEED_REAL ?= true
SEED_REAL_MAX_BYTES ?= 5368709120
SEED_REAL_IMAGES ?=
SEED_REAL_CACHE ?= deploy/compose/real-image-cache.tar
SEED_REAL_CATALOG ?= test/fixtures/nexus/real-image-catalog.yaml
SEED_REAL_MANIFEST ?= deploy/compose/real-image-cache.manifest.md
SEED_REAL_PULL ?= true

format:
	gofmt -w ./cmd ./internal

test:
	$(GO_TEST) ./...

test-integration:
	test -n "$$NEXUS_LENS_TEST_DATABASE_URL"
	$(GO_TEST) ./internal/indexing

web-deps: $(WEB_NODE_MODULES)

$(WEB_NODE_MODULES): web/package.json web/package-lock.json
	cd web && npm ci

typecheck: web-deps
	cd web && npm run typecheck

openapi: web-deps
	cd web && npm run generate:api

openapi-check: openapi
	git diff --exit-code -- web/src/api/types.ts

sqlc:
	env -u GOROOT go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate

migrate:
	env -u GOROOT go run github.com/pressly/goose/v3/cmd/goose@v3.27.1 -dir db/migrations postgres "$(DB_URL)" up

helm-lint:
	helm lint ./charts/nexus-lens

helm-template:
	helm template nexus-lens ./charts/nexus-lens

helm-template-fixtures:
	for values in charts/nexus-lens/ci-values/*.yaml; do helm template nexus-lens ./charts/nexus-lens -f "$$values" >/dev/null; done

helm-test:
	test -n "$$(command -v helm)"
	GOCACHE=$${GOCACHE:-/tmp/nexus-lens-go-build-cache} $(GO_TEST) ./test/helm

helm-check: helm-lint helm-template helm-template-fixtures helm-test

helm-package:
	helm package ./charts/nexus-lens --destination /tmp

check: test typecheck openapi-check helm-check

up: web-deps
	docker compose -f $(COMPOSE_FILE) up -d postgres nexus api worker
	docker compose -f $(COMPOSE_FILE) stop web >/dev/null 2>&1 || true
	@if [ -f "$(WEB_DEV_PID)" ] && kill -0 "$$(cat "$(WEB_DEV_PID)")" >/dev/null 2>&1; then \
		echo "web dev server already running at http://$(WEB_DEV_HOST):$(WEB_DEV_PORT)"; \
	else \
		cd web && nohup npm run dev -- --host $(WEB_DEV_HOST) --port $(WEB_DEV_PORT) >"$(WEB_DEV_LOG)" 2>&1 & echo $$! >"$(WEB_DEV_PID)"; \
		echo "started web dev server at http://$(WEB_DEV_HOST):$(WEB_DEV_PORT)"; \
		echo "web log: $(WEB_DEV_LOG)"; \
	fi
	@echo "API: http://localhost:5601"
	@echo "Nexus: http://localhost:8081"
	@echo "Postgres: localhost:5432 db/user/password nexus_lens/nexus_lens/nexus_lens"
	@echo "Login: admin / admin"

down:
	@if [ -f "$(WEB_DEV_PID)" ]; then \
		if kill -0 "$$(cat "$(WEB_DEV_PID)")" >/dev/null 2>&1; then \
			kill "$$(cat "$(WEB_DEV_PID)")"; \
			echo "stopped web dev server"; \
		fi; \
		rm -f "$(WEB_DEV_PID)"; \
	fi
	docker compose -f $(COMPOSE_FILE) down

status:
	@if [ -f "$(WEB_DEV_PID)" ] && kill -0 "$$(cat "$(WEB_DEV_PID)")" >/dev/null 2>&1; then \
		echo "web dev server: running at http://$(WEB_DEV_HOST):$(WEB_DEV_PORT)"; \
	else \
		echo "web dev server: stopped"; \
	fi
	docker compose -f $(COMPOSE_FILE) ps

bootstrap: fixture-wait fixture-capture-password fixture-accept-eula fixture-enable-docker-realm fixture-create-repos
	@echo "Nexus bootstrap complete. Run make fixture-seed to push the small Docker fixture."

seed: bootstrap fixture-seed fixture-index
	@echo "Local seed complete. Refresh http://$(WEB_DEV_HOST):$(WEB_DEV_PORT) to see indexed data."

seed-offline: SEED_REAL_PULL=false
seed-offline: seed

seed-cache:
	NEXUS_LENS_FIXTURE_REAL_MAX_BYTES="$(SEED_REAL_MAX_BYTES)" NEXUS_LENS_FIXTURE_REAL_IMAGE_REFS="$(SEED_REAL_IMAGES)" NEXUS_LENS_FIXTURE_REAL_CACHE_TAR="$(SEED_REAL_CACHE)" NEXUS_LENS_FIXTURE_REAL_CATALOG="$(SEED_REAL_CATALOG)" NEXUS_LENS_FIXTURE_REAL_MANIFEST="$(SEED_REAL_MANIFEST)" test/fixtures/nexus/cache-real-images.sh

urls:
	@echo "Web UI:   http://$(WEB_DEV_HOST):$(WEB_DEV_PORT)"
	@echo "API:      http://localhost:5601"
	@echo "Nexus UI: http://localhost:8081"
	@echo "Docker registries:"
	@echo "  docker-hosted-a: localhost:5001"
	@echo "  docker-hosted-b: localhost:5002"
	@echo "Postgres:"
	@echo "  host=localhost port=5432 db=nexus_lens user=nexus_lens password=nexus_lens"

postgres:
	docker compose -f $(COMPOSE_FILE) exec postgres psql -U nexus_lens -d nexus_lens

nexus-password:
	@test -f deploy/compose/.nexus-admin-password
	@printf 'Nexus admin password: '
	@cat deploy/compose/.nexus-admin-password

e2e: compose-e2e

compose-e2e: e2e-live-cleanup

e2e-live-cleanup:
	test/e2e/live-cleanup.sh

docker-build:
	docker build -f Dockerfile.api -t nexus-lens-api:dev .
	docker build -f Dockerfile.worker -t nexus-lens-worker:dev .

compose-config:
	docker compose -f $(COMPOSE_FILE) config

fixture-wait:
	test/fixtures/nexus/wait-for-nexus.sh

fixture-capture-password:
	test/fixtures/nexus/capture-admin-password.sh

fixture-accept-eula:
	test/fixtures/nexus/accept-eula.sh

fixture-enable-docker-realm:
	test/fixtures/nexus/enable-docker-realm.sh

fixture-create-repos:
	test/fixtures/nexus/create-docker-repos.sh

fixture-seed:
	NEXUS_LENS_FIXTURE_SCALE=$(SEED_SCALE) NEXUS_LENS_FIXTURE_IMAGE_COUNT="$(SEED_IMAGE_COUNT)" NEXUS_LENS_FIXTURE_TAGS_PER_IMAGE="$(SEED_TAGS_PER_IMAGE)" NEXUS_LENS_FIXTURE_COMMON_IMAGES="$(SEED_COMMON)" NEXUS_LENS_FIXTURE_COMPLEX_TAGS="$(SEED_COMPLEX)" NEXUS_LENS_FIXTURE_COMPLEX_TAG_COUNT="$(SEED_COMPLEX_TAGS)" NEXUS_LENS_FIXTURE_REAL_IMAGES="$(SEED_REAL)" NEXUS_LENS_FIXTURE_REAL_MAX_BYTES="$(SEED_REAL_MAX_BYTES)" NEXUS_LENS_FIXTURE_REAL_IMAGE_REFS="$(SEED_REAL_IMAGES)" NEXUS_LENS_FIXTURE_REAL_CACHE_TAR="$(SEED_REAL_CACHE)" NEXUS_LENS_FIXTURE_REAL_CATALOG="$(SEED_REAL_CATALOG)" NEXUS_LENS_FIXTURE_REAL_PULL="$(SEED_REAL_PULL)" test/fixtures/nexus/build-and-push-images.sh

fixture-index:
	test/fixtures/nexus/index-seed.sh

fixture-seed-medium:
	NEXUS_LENS_FIXTURE_SCALE=medium NEXUS_LENS_FIXTURE_COMMON_IMAGES="$(SEED_COMMON)" NEXUS_LENS_FIXTURE_COMPLEX_TAGS="$(SEED_COMPLEX)" NEXUS_LENS_FIXTURE_COMPLEX_TAG_COUNT="$(SEED_COMPLEX_TAGS)" NEXUS_LENS_FIXTURE_REAL_IMAGES="$(SEED_REAL)" NEXUS_LENS_FIXTURE_REAL_MAX_BYTES="$(SEED_REAL_MAX_BYTES)" NEXUS_LENS_FIXTURE_REAL_IMAGE_REFS="$(SEED_REAL_IMAGES)" NEXUS_LENS_FIXTURE_REAL_CACHE_TAR="$(SEED_REAL_CACHE)" NEXUS_LENS_FIXTURE_REAL_CATALOG="$(SEED_REAL_CATALOG)" NEXUS_LENS_FIXTURE_REAL_PULL="$(SEED_REAL_PULL)" test/fixtures/nexus/build-and-push-images.sh

fixture-seed-large:
	NEXUS_LENS_FIXTURE_SCALE=large NEXUS_LENS_FIXTURE_COMMON_IMAGES="$(SEED_COMMON)" NEXUS_LENS_FIXTURE_COMPLEX_TAGS="$(SEED_COMPLEX)" NEXUS_LENS_FIXTURE_COMPLEX_TAG_COUNT="$(SEED_COMPLEX_TAGS)" NEXUS_LENS_FIXTURE_REAL_IMAGES="$(SEED_REAL)" NEXUS_LENS_FIXTURE_REAL_MAX_BYTES="$(SEED_REAL_MAX_BYTES)" NEXUS_LENS_FIXTURE_REAL_IMAGE_REFS="$(SEED_REAL_IMAGES)" NEXUS_LENS_FIXTURE_REAL_CACHE_TAR="$(SEED_REAL_CACHE)" NEXUS_LENS_FIXTURE_REAL_CATALOG="$(SEED_REAL_CATALOG)" NEXUS_LENS_FIXTURE_REAL_PULL="$(SEED_REAL_PULL)" test/fixtures/nexus/build-and-push-images.sh

fixture-mutate:
	test/fixtures/nexus/mutate-tags.sh

fixture-probe:
	test/fixtures/nexus/probe.sh
