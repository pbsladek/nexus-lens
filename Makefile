.PHONY: format test test-integration web-deps typecheck openapi sqlc migrate helm-lint helm-template helm-test helm-check e2e e2e-live-cleanup docker-build compose-config fixture-wait fixture-capture-password fixture-accept-eula fixture-enable-docker-realm fixture-create-repos fixture-seed fixture-seed-medium fixture-seed-large fixture-mutate fixture-probe

DB_URL ?= postgres://nexus_lens:nexus_lens@localhost:5432/nexus_lens?sslmode=disable
GO_TEST ?= env -u GOROOT go test
WEB_NODE_MODULES := web/node_modules/.package-lock.json

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

sqlc:
	env -u GOROOT go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate

migrate:
	env -u GOROOT go run github.com/pressly/goose/v3/cmd/goose@v3.27.1 -dir db/migrations postgres "$(DB_URL)" up

helm-lint:
	helm lint ./charts/nexus-lens

helm-template:
	helm template nexus-lens ./charts/nexus-lens

helm-test:
	test -n "$$(command -v helm)"
	GOCACHE=$${GOCACHE:-/tmp/nexus-lens-go-build-cache} $(GO_TEST) ./test/helm

helm-check: helm-lint helm-template helm-test

e2e:
	@echo "E2E is not wired yet"

e2e-live-cleanup:
	test/e2e/live-cleanup.sh

docker-build:
	docker build -f Dockerfile.api -t nexus-lens-api:dev .
	docker build -f Dockerfile.worker -t nexus-lens-worker:dev .

compose-config:
	docker compose -f deploy/compose/compose.yaml config

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
	test/fixtures/nexus/build-and-push-images.sh

fixture-seed-medium:
	NEXUS_LENS_FIXTURE_SCALE=medium test/fixtures/nexus/build-and-push-images.sh

fixture-seed-large:
	NEXUS_LENS_FIXTURE_SCALE=large test/fixtures/nexus/build-and-push-images.sh

fixture-mutate:
	test/fixtures/nexus/mutate-tags.sh

fixture-probe:
	test/fixtures/nexus/probe.sh
