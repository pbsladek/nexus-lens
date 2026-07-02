# E2E Fixtures

The first fixture creates:

- Two hosted Docker repositories.
- Shared layers across images and repositories.
- Tags covering SemVer, SHA, PR, feature branch, `latest`, and `stable`.
- Mutations for moved, deleted, and recreated tags.

## Local Commands

Start the stack:

```bash
docker compose -f deploy/compose/compose.yaml up -d
```

After Nexus initializes, copy the initial admin password from the container into `deploy/compose/.nexus-admin-password`, or export `NEXUS_PASSWORD`.

```bash
test/fixtures/nexus/wait-for-nexus.sh
test/fixtures/nexus/capture-admin-password.sh
test/fixtures/nexus/accept-eula.sh
test/fixtures/nexus/enable-docker-realm.sh
test/fixtures/nexus/create-docker-repos.sh
test/fixtures/nexus/build-and-push-images.sh
test/fixtures/nexus/probe.sh
test/fixtures/nexus/mutate-tags.sh
```

The repository connectors are exposed as:

- `localhost:5001` for `docker-hosted-a`
- `localhost:5002` for `docker-hosted-b`

## Live Cleanup E2E

The disposable fixture can run the destructive cleanup path end to end:

```bash
make e2e-live-cleanup
```

That command starts the Compose services, prepares and seeds Nexus, starts the API and worker with `NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP=true`, runs a scoped full reconcile, approves a cleanup execution for `docker-hosted-a/nexus-lens/app:pr-123`, waits for post-delete verification and reconcile, and checks both the Nexus registry tag list and PostgreSQL index state. The script restores the API and worker to the default read-only cleanup setting before it exits.

## Scale Fixture Contract

Keep the default fixture small. It should be fast enough for local smoke tests and CI.

Add larger fixture modes behind explicit environment variables or Make targets. A larger fixture should preserve the same behavioral coverage while increasing row counts:

- Multiple hosted Docker repositories with different layouts.
- Shared layers across repositories and images.
- Tags that move, delete, and recreate.
- SemVer, long SHA, short SHA, PR, feature branch, `latest`, and `stable` tags.
- Enough repeated tags and assets to exercise pagination, explorer filters, cleanup simulation, verification, and trend snapshots.

Suggested modes:

```text
small: default CI fixture, selected by fixture-seed
medium: thousands of tags for local query review, selected by fixture-seed-medium
large: operator-run scale fixture for millions-row planning, selected by fixture-seed-large
```

The scale fixture should not require extra services beyond Docker Compose, Nexus, PostgreSQL, API, worker, and web. Destructive cleanup tests may delete from the seeded Nexus because the fixture is disposable.

Scale can also be tuned directly:

```bash
NEXUS_LENS_FIXTURE_SCALE=medium \
NEXUS_LENS_FIXTURE_IMAGE_COUNT=50 \
NEXUS_LENS_FIXTURE_TAGS_PER_IMAGE=100 \
test/fixtures/nexus/build-and-push-images.sh
```
