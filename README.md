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

- [Go 1.25+](https://go.dev/doc/install)
- **PostgreSQL 16 or newer** — the `IS JSON` predicate is used; the bundled
  compose file pins 18
- Docker, if you want the bundled Postgres rather than your own
- `pg_dump` / `psql`, but only for the backup feature (they can run inside the
  container — see [POSTGRES.md](POSTGRES.md#backups))

## Quick start

```bash
docker-compose up -d postgres
```

```bash
go run ./examples/base serve
```

That's it — the built-in defaults match the bundled compose file, so a fresh
clone runs with no configuration at all.

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
