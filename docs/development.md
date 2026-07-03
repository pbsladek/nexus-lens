# Development

## Go

Use `make test` for the standard local test command:

```sh
make test
```

The Makefile clears `GOROOT` before invoking Go so a stale shell value from a
version manager does not make the compiler and standard library disagree.

This repo also includes `.mise.toml` with an empty `GOROOT` override. If your
shell loads mise automatically and you want bare `go test ./...` to work from
this directory, trust the repo config once:

```sh
mise trust .mise.toml
```

After that, mise will clear the inherited `GOROOT` value before running Go in
this repo.

## Frontend

Use `make typecheck` for TypeScript checks:

```sh
make typecheck
```

The target depends on `web-deps`, which runs `npm ci` from `web/package-lock.json`
when `web/node_modules` is missing or stale.

Use `make openapi-check` after changing `api/openapi.yaml`. It regenerates
`web/src/api/types.ts` and fails if the generated TypeScript has uncommitted
drift.

## Local Server

Use `make up` to start the local testing interface:

```sh
make up
```

This starts Postgres, Nexus, the API, and the worker through Docker Compose,
stops the placeholder Compose web container, and starts the Vite frontend at
`http://localhost:5173`. The default login is `admin` / `admin`.

The local Compose stack uses development credentials, not Kubernetes Secrets.
Postgres is configured with `nexus_lens` for database, username, and password.
Nexus starts as the official container and the fixture helper captures the admin
password into `deploy/compose/.nexus-admin-password`; the API and worker mount
that file read-only.

To load demo data into Nexus and the app database:

```sh
make seed
```

That waits for Nexus, captures the admin password, accepts the EULA, enables the
Docker bearer token realm, creates the two hosted Docker repositories, pushes
the small Docker fixture, and starts a full reconcile through the API. When that
run completes, Postgres has indexed repository, image, tag, manifest, layer, and
tag-pattern data for the local UI.

`make seed` is configurable. By default it seeds the small app fixture, a
lightweight common catalog of tiny synthetic images, a high-cardinality
tag-pattern fixture, and a capped real-image catalog. The synthetic images are
named like common public images
(`library/alpine`, `library/debian`, `library/nginx`, `library/node`,
`library/python`, `library/busybox`, and similar) with version, `slim`,
`alpine`, `latest`, and stable-style tags. They are generated locally from `scratch`, so the names are
realistic for UI testing while the storage footprint stays small.

The high-cardinality tag-pattern fixture creates tiny `nexus-lens/tag-patterns`
images with many tags that exercise frontend filtering and pattern discovery:
SemVer releases, release candidates, date builds, PR tags, branch-derived tags,
branch/version/hash tags, `git-<hash>` suffixes, dirty builds, and
`SNAPSHOT` variants such as
`branch-name-variations-a1b2c3d-SNAPSHOT` and
`feature-checkout-redesign-v2.8.0-a1b2c3d`.

The real-image catalog is maintained in
`test/fixtures/nexus/real-image-catalog.yaml`. It includes small real base
images, runtime images, database images, builder images, registry, and Nexus
variants, and each source image can publish multiple target tags into the local
Nexus fixture. This gives the UI realistic manifest, config, layer, image-size,
and tag-pattern data without relying only on synthetic scratch images.

Real image pushes are capped by `SEED_REAL_MAX_BYTES`, which defaults to
`5368709120` bytes, or 5 GiB. The seeder checks each pulled image with
`docker image inspect` and skips an image if adding it would exceed the cap.

To avoid pulling from Docker Hub while on a slow or metered network, create a
local tar cache once while you have a good connection:

```sh
make seed-cache
```

That writes `deploy/compose/real-image-cache.tar` by default and also writes
`deploy/compose/real-image-cache.manifest.md` with the selected images, skipped
images, Docker-reported size, digest/ID, Nexus target path, and extra target
tags. It also updates the `cache` summary in
`test/fixtures/nexus/real-image-catalog.yaml` when using the catalog path,
keeping the expected image count and Docker-reported byte total beside the
catalog. The tar and manifest are generated local artifacts and are ignored by
git. Later, seed from that tar without pulling upstream images:

```sh
make seed-offline
```

You can move the cache tar or use a different path:

```sh
make seed-cache SEED_REAL_CACHE=/Volumes/cache/nexus-lens-real-images.tar
make seed-offline SEED_REAL_CACHE=/Volumes/cache/nexus-lens-real-images.tar
```

Examples:

```sh
make seed SEED_COMMON=false
make seed SEED_COMPLEX=false
make seed SEED_COMPLEX_TAGS=200
make seed SEED_REAL=false
make seed SEED_REAL_MAX_BYTES=1073741824
make seed SEED_REAL_IMAGES="alpine:3.24 busybox:1.38 nginx:1.29.8-alpine"
make seed SEED_SCALE=medium
make seed SEED_SCALE=large
make seed SEED_SCALE=medium SEED_IMAGE_COUNT=50 SEED_TAGS_PER_IMAGE=40
```

Seed knobs:

- `SEED_COMMON=true|false`: include the lightweight common catalog.
- `SEED_COMPLEX=true|false`: include the high-cardinality tag-pattern fixture.
- `SEED_COMPLEX_TAGS=N`: generated complex tags per tag-pattern fixture image,
  in addition to baseline hand-picked tags.
- `SEED_REAL=true|false`: include the capped real-image catalog.
- `SEED_REAL_MAX_BYTES=N`: cap real-image Docker-reported bytes before pushing.
- `SEED_REAL_IMAGES="ref ref ..."`: override the real-image refs to pull.
- `SEED_REAL_CATALOG=path`: YAML catalog for real-image source refs, target
  paths, extra target tags, and notes.
- `SEED_REAL_CACHE=path`: Docker save/load tar path for real-image caching.
- `SEED_REAL_MANIFEST=path`: generated real-image cache manifest path.
- `SEED_REAL_PULL=true|false`: pull real images when seeding; `seed-offline`
  sets this to `false`.
- `SEED_SCALE=small|medium|large`: add synthetic scale images.
- `SEED_IMAGE_COUNT=N`: override generated scale image count.
- `SEED_TAGS_PER_IMAGE=N`: override generated scale tag count.

If you only want to prepare Nexus without pushing/indexing data, use:

```sh
make bootstrap
```

Use these helpers to inspect or stop it:

```sh
make status
make urls
make postgres
make nexus-password
make down
```

To clean up retired fixture tags after seed catalog changes, start with a dry
run:

```sh
test/fixtures/nexus/prune-old-seed-data.sh --local-docker
```

Apply Nexus deletes with `--apply`. Add `--local-docker` when you also want to
remove matching old upstream image tags from the local Docker daemon.

The Vite process writes its PID and log to `/tmp/nexus-lens-web-dev.pid` and
`/tmp/nexus-lens-web-dev.log`.

## Full Check

Use `make check` before opening a PR:

```sh
make check
```

The target runs Go tests, frontend typecheck, OpenAPI generated-type drift
checks, Helm lint/render checks, Helm fixture renders, and Go chart tests.

## Helm

Use `make helm-check` before changing the chart:

```sh
make helm-check
```

The target runs `helm lint`, renders the default chart, and runs Go-based chart
tests that assert rendered Deployments, Service selectors, Secret references,
optional metrics resources, and schema rejection for invalid values.

Chart fixture values live under `charts/nexus-lens/ci-values/` and are rendered
by `make helm-check`.

See [Helm Chart Improvement Plan](helm-chart-improvement-plan.md) for the
planned chart hardening and common configuration features.

## Compose E2E

Use `make e2e` to run the Docker Compose live cleanup flow. It starts the
Postgres and Nexus fixture, seeds two hosted Docker repositories, runs a scoped
full reconcile, simulates cleanup, performs a destructive delete against the
seeded Nexus fixture, and verifies post-delete index state.
