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
