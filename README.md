# PocketBase on PostgreSQL

A fork of [PocketBase](https://pocketbase.io) with **SQLite replaced by PostgreSQL**.

There is no SQLite code path left and no build tag to switch back — this is a
one-way port, not a dual-database abstraction.

> **Not affiliated with the upstream project.** For upstream PocketBase, see
> [pocketbase/pocketbase](https://github.com/pocketbase/pocketbase). Please do
> not report issues found here to the upstream maintainers.

Everything else PocketBase offers is unchanged:

- database with **realtime subscriptions**
- built-in **files and users management**
- convenient **Admin dashboard UI**
- simple **REST-ish API**

**For general documentation and examples, upstream's docs still apply:
https://pocketbase.io/docs.** For everything specific to this fork — connection
settings, behavioural differences, backups — see **[POSTGRES.md](POSTGRES.md)**.

Fork-specific changes, and a record of what was taken from each upstream
release, live in **[CHANGELOG_FORK.md](CHANGELOG_FORK.md)**. `CHANGELOG.md` is
kept byte-identical to upstream so that pulling their changes never conflicts
there.

> [!WARNING]
> PocketBase is under active development and full backward compatibility is not
> guaranteed before v1.0.0. This fork adds its own deliberate API breaks on top
> (see [Behaviour differences](#behaviour-differences)).

## Requirements

**To run it:** Docker, and nothing else. The bundle below brings its own
PostgreSQL.

**To build from source or use it as a Go library:** [Go 1.25+](https://go.dev/doc/install)
and a PostgreSQL 16 or newer server (the `IS JSON` predicate is used; the bundled
compose file pins 18).

## Quick start

You do not need this repository, Go, or a database. Three commands on any
machine with Docker:

```bash
curl -o compose.yaml https://raw.githubusercontent.com/mwakalinga/pocketbase-postgres/master/docker-compose.prod.yml
```

```bash
curl -o .env https://raw.githubusercontent.com/mwakalinga/pocketbase-postgres/master/.env.example
```

Open `.env` and set `POSTGRES_PASSWORD` — the app refuses to start while it is
the example value. Generate one with `openssl rand -base64 24`. Everything else
has a working default.

```bash
docker compose up -d
```

That is it. The dashboard is at **http://localhost:8090/_/**.

Create the first superuser:

```bash
docker compose exec pocketbase pb superuser upsert you@example.com yourpassword
```

> Use `pb`, not `pocketbase`, for commands inside the container. `docker compose
> exec` bypasses the image's entrypoint, so the bare binary would run without
> `--dir` and fail confusingly. `pb` applies the flags the server uses.

Check it is healthy at any time:

```bash
curl http://localhost:8090/api/health
```

### What you just started

- **Two containers**: the app, and PostgreSQL. Postgres is *not* published to the
  host — only the app reaches it, over a private network.
- **Two volumes**: `pgdata` holds the database (your records, collections and
  settings) and `pbdata` holds uploaded files and generated backups. **Both** are
  needed for a full restore — copying `pbdata` alone is not a backup.
- The app runs as a non-root user, and carries `pg_dump`/`psql` matching the
  server version so backups work from inside the container.

Saving the file as `compose.yaml` is why the commands above have no
`-f` flag. If you cloned the repository instead, the file is
`docker-compose.prod.yml` and every command needs
`-f docker-compose.prod.yml`.

### Putting it on the internet

The bundle publishes port 8090 over plain HTTP, which is fine behind a proxy and
wrong on its own. For a public deployment, terminate TLS in front of it — Caddy,
nginx or your platform's load balancer — and forward to `pocketbase:8090`.

PocketBase's built-in Let's Encrypt support (`serve yourdomain.com`) needs ports
80 and 443, which the bundle does not publish, so it is not the path of least
resistance here.

Also set `PB_IMAGE_TAG` in `.env` to a specific release rather than leaving it at
`latest`, so a restart cannot silently move you to a new version.

### Troubleshooting

**`port is already allocated`** — something else uses 8090. Set `PB_PORT=8091`
in `.env` and run `docker compose up -d` again.

**`denied` or `unauthorized` when pulling the image** — the package may still be
private. Either make it public in the repository's package settings, or
`docker login ghcr.io` first.

**`refusing to start: POSTGRES_PASSWORD is still the example value`** — working
as intended. Set a real password in `.env`.

**`permission denied` talking to Docker** — your user is not in the `docker`
group. Use `sudo`, or add yourself and log out and back in.

**Nothing on 8090** — check both containers are up and read the logs:

```bash
docker compose ps
```

```bash
docker compose logs pocketbase
```

**Start over completely** — this deletes the database and all uploads:

```bash
docker compose down -v
```

## Configuring the database

Resolution order, first match wins:

1. `--dbUrl` flag
2. `PB_DB_URL` environment variable
3. `db.json` in the data directory
4. built-in defaults (`pocketbase:pocketbase@localhost:5432/pocketbase`)

For a persistent change without managing env variables, use the `db` command:

```bash
pocketbase db show
```

```bash
pocketbase db set --host db.internal --port 5432 --user app --password secret --dbName app
```

```bash
pocketbase db test
```

`set` updates only the flags you pass and warns if a higher-priority source is
currently overriding the stored values. These commands intentionally work even
when the database is unreachable — that's what they're for.

Full details, including the security caveat about storing passwords on disk, are
in [POSTGRES.md](POSTGRES.md#configuring-the-connection).

## Updating

**Take a backup first.** An update can carry migrations, which run automatically
on the next start.

**Docker:** update by image tag, not with the `update` command — `ghupdate`
would rewrite the binary *inside* the running container, which works until the
container is recreated and then silently reverts.

```bash
docker compose -f docker-compose.prod.yml pull && docker compose -f docker-compose.prod.yml up -d
```

**Binary:** self-update from this fork's GitHub releases.

```bash
./pocketbase update
```

> The plugin defaults to `pocketbase/pocketbase`. This fork points it at its own
> repository — otherwise `update` would fetch the upstream **SQLite** build and
> overwrite the binary in place, and the next start would find no SQLite data
> and look like total data loss.

## Behaviour differences

These are deliberate and documented in full in
[POSTGRES.md](POSTGRES.md#behaviour-differences-from-upstream-sqlite-pocketbase):

| Area | Behaviour |
| --- | --- |
| `strftime` in filters | Translated to `to_char` for common substitutions; **errors** on unsupported ones rather than silently returning different data |
| `@rowid` | Backed by a real `BIGSERIAL` column, preserving insertion order |
| `LIKE` | Becomes `ILIKE`, keeping filters case-insensitive |
| `COLLATE NOCASE` | Becomes `LOWER()` functional indexes — no `citext` extension needed |
| JSON extraction | Returns text, so comparing a JSON-stored number to a SQL number may differ from upstream |
| View collections | Validated at **startup**, failing loudly instead of erroring later at runtime |
| Backups | `pg_dump` / `psql` instead of a file copy |

## Overview

### Use as a Go framework/toolkit

The module path is still `github.com/pocketbase/pocketbase`, so importing this
fork requires a `replace` directive — otherwise Go resolves upstream and you get
SQLite.

1. Create a new project directory with the following `main.go` file inside it:

    ```go
    package main

    import (
        "log"

        "github.com/pocketbase/pocketbase"
        "github.com/pocketbase/pocketbase/core"
    )

    func main() {
        app := pocketbase.New()

        app.OnServe().BindFunc(func(se *core.ServeEvent) error {
            // registers new "GET /hello" route
            se.Router.GET("/hello", func(re *core.RequestEvent) error {
                return re.String(200, "Hello world!")
            })

            return se.Next()
        })

        if err := app.Start(); err != nil {
            log.Fatal(err)
        }
    }
    ```

2. Initialize the module and point it at this fork:

    ```bash
    go mod init myapp
    ```

    ```bash
    go mod edit -replace github.com/pocketbase/pocketbase=github.com/mwakalinga/pocketbase-postgres@latest
    ```

    ```bash
    go mod tidy
    ```

    > [!TIP]
    > For reproducible builds, pin a specific tag or commit instead of
    > `@latest`, eg. `...pocketbase-postgres@v0.39.9-postgres.1`.

    To develop against a local checkout instead, replace with the path:
    `go mod edit -replace github.com/pocketbase/pocketbase=../pocketbase-postgres`

3. Start the application with `go run main.go serve` (a reachable Postgres is
   required — see [Quick start](#quick-start)).

4. To build a statically linked executable, run `CGO_ENABLED=0 go build`.

_For the framework API itself, upstream's [Extend with Go](https://pocketbase.io/docs/go-overview/) guide still applies._

### Building for production

```bash
make build              # host platform  -> .builds/pocketbase
make build-linux        # linux/amd64    -> .builds/pocketbase-linux-amd64
make build-linux-arm64  # linux/arm64    -> .builds/pocketbase-linux-arm64
```

Without `make` (eg. on Windows), the same build directly:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/pocketbase/pocketbase.Version=$(git describe --tags --always)" -o .builds/pocketbase ./examples/base
```

`-s -w` strips the symbol table and DWARF data, `-trimpath` removes local
filesystem paths, and the version is stamped from `git describe` (override with
`make build VERSION=v1.2.3`).

The admin dashboard is compiled into the binary from `ui/dist`, which is
committed — no Node build step is needed to produce a working server.

The Postgres driver (`pgx`) is pure Go, so unlike upstream there is no
driver-specific build target list: `CGO_ENABLED=0` produces a statically linked
binary and cross-compiles to any platform Go itself supports.

### Running in production

The binary needs nothing but a reachable PostgreSQL:

```bash
PB_DB_URL="postgres://user:pass@db.internal:5432/pocketbase?sslmode=require" \
  ./pocketbase serve --http 0.0.0.0:8090
```

Checklist:

- **Set the connection explicitly.** The built-in defaults point at
  `localhost:5432` with the credentials `pocketbase:pocketbase` — convenient
  for local development, wrong for production. Use `PB_DB_URL` (never written
  to disk) or `pocketbase db set`.
- **Use `sslmode=require`** or stricter for any non-local database.
- **Set `--encryptionEnv`** to the name of an env variable holding a 32
  character key, so that stored settings (SMTP and S3 credentials) are
  encrypted at rest.
- **Terminate TLS.** Passing a domain enables automatic Let's Encrypt
  certificates (`./pocketbase serve yourdomain.com`), or put a reverse proxy in
  front and keep the server on `--http`.
- **Back up the database, not just `pb_data`.** This is the big operational
  change from upstream: records, collections and settings now live in
  PostgreSQL, while `pb_data` holds only uploaded files and generated backups.
  Copying `pb_data` alone no longer captures your data. See
  [POSTGRES.md](POSTGRES.md#backups).
- **Note the default data directory** is resolved next to the executable, so a
  binary in `.builds/` uses `.builds/pb_data`. Pass `--dir` explicitly in
  deployments.

### Testing

The suite needs its own Postgres, provided by the compose file on port **5433**
so a run never touches your dev data:

```bash
docker-compose --profile test up -d postgres-test
```

```bash
go test ./...
```

> Always pass `--profile test`. A plain `docker-compose up -d postgres` treats
> the profile-gated `postgres-test` service as an orphan and **removes** it,
> after which the whole suite fails with connection-refused errors that look
> like code regressions.

Each test process builds one migrated and seeded template database and clones it
per test app, which keeps tests isolated while running in parallel. See
[POSTGRES.md](POSTGRES.md#running-the-tests) for details and how to clean up
leftovers.

## Security

Vulnerabilities in **upstream PocketBase** should be reported to
**support at pocketbase.io**, per [upstream's policy](https://github.com/pocketbase/pocketbase/security).

Issues specific to **this fork** (anything in the Postgres layer) should be
raised here instead — upstream cannot act on them.

## License

PocketBase is licensed under the [MIT License](LICENSE.md), and so is this fork.
Upstream retains copyright for the original work.
