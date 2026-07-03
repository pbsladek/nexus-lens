# Nexus Lens

Nexus Lens indexes hosted Docker repositories in Sonatype Nexus 3 and presents
the results in a small web UI. It is built for Nexus 3.88.0+ and focuses on
Docker/OCI tags, manifests, layers, image size, tag patterns, cleanup analysis,
and verification after deletes.

The project is Kubernetes-friendly but does not require an operator. The backend
is Go, the frontend is TypeScript with Vite, and indexed data is stored in
PostgreSQL. Postgres is expected to be provided by the user, for example by CNPG
or an external database. Nexus and Postgres credentials are supplied through
configuration and secrets.

## What It Does

- Indexes one or more configured Nexus instances.
- Supports hosted Docker repositories.
- Uses Nexus REST APIs and Docker Registry APIs.
- Stores repository, image, tag, manifest, layer, asset, schedule, and scan-run
  data in PostgreSQL.
- Classifies tags with built-in and configurable tag patterns.
- Provides local Docker Compose fixtures for Nexus, Postgres, the API, worker,
  and frontend development.
- Ships a Helm chart for Kubernetes deployment.

## Local Development

Start the local stack:

```sh
make up
```

Seed Nexus and index demo data:

```sh
make seed
```

Useful local URLs:

- Web UI: `http://localhost:5173`
- API: `http://localhost:5601`
- Nexus: `http://localhost:8081`
- Docker registries: `localhost:5001` and `localhost:5002`

Default local login:

```text
admin / admin
```

Run checks:

```sh
make check
```

More details are in [docs/development.md](docs/development.md).

## License

Nexus Lens is released under the MIT License. See [LICENSE](LICENSE).
