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
