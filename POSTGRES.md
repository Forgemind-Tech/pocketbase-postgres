# PocketBase on Postgres

This fork replaces SQLite with PostgreSQL. There is no SQLite code path left and
no build tag to switch back.

## Requirements

- Go 1.25+
- PostgreSQL 16 or newer (the `IS JSON` predicate is used); the bundled compose
  file pins **18**
- `pg_dump` / `psql`, but only if you use the backup feature — they do not have
  to be installed on the host, see [Backups](#backups)

## Quick start

```bash
docker-compose up -d postgres
```

```bash
go run ./examples/base serve
```

The server connects to `postgres://pocketbase:pocketbase@localhost:5432/pocketbase`
by default.

## Configuring the connection

Resolution order, first match wins:

1. `--dbUrl` flag
2. `PB_DB_URL` environment variable
3. `db.json` in the data directory (managed by the `db` command below)
4. the built-in defaults

```bash
go run ./examples/base serve --dbUrl "postgres://user:pass@db.example.com:5432/pocketbase?sslmode=require"
```

The built-in defaults are `pocketbase:pocketbase@localhost:5432/pocketbase`
with `sslmode=disable`, matching the bundled compose file — so a fresh checkout
runs with no configuration at all.

### The `db` command

For a persistent change without managing env variables:

```bash
pocketbase db show
```

```bash
pocketbase db set --host db.internal --port 5432 --user app --password secret --dbName app
```

```bash
pocketbase db test
```

`set` updates **only the flags you pass**, leaving the rest untouched, and warns
if a higher-priority source is currently overriding the saved values. `show`
masks the password. `test` opens a real connection and reports the server
version.

These commands deliberately **skip bootstrap** — they exist to fix an
unreachable database, so they must not require a working one first.

For connection strings the individual fields can't express (client
certificates, extra libpq parameters), `--url` stores one verbatim and takes
precedence over the other fields; `--clearUrl` removes it again.

> **Security:** `db.json` stores the password in plain text. It is written with
> owner-only permissions, but Go only maps the read-only bit on Windows, so the
> mode has no effect there. Where a secret store is available, prefer
> `PB_DB_URL` — it takes precedence over the file and never touches disk.

A malformed or invalid `db.json` is reported as a startup error rather than
being silently replaced by the defaults, which would quietly connect to the
wrong database. Connection failures now name the connection string in use
(password masked), where it came from, and how to change it.

## Schema layout

Both stores live in one database, since Postgres cannot attach a second file the
way SQLite does:

| Schema   | Contents                                     |
| -------- | -------------------------------------------- |
| `public` | collections, records, settings, migrations   |
| `pb_aux` | logs (`_logs`)                               |

The aux schema is created automatically on first boot.

## Running the tests

The suite needs its own Postgres. The compose file provides one on port **5433**
with its own volume, so a run never touches your dev data:

```bash
docker-compose --profile test up -d postgres-test
```

```bash
go test ./...
```

> Always pass `--profile test` when running compose commands. A plain
> `docker-compose up -d postgres` treats the profile-gated `postgres-test`
> service as an orphan and **removes** it, after which the whole suite fails
> with connection-refused errors that look like code regressions.

Override the target with `PB_TEST_DB_URL` if needed.

Each test process builds one fully migrated and seeded **template database**
(`pb_test_template_<pid>`), and every test application then gets its own
database cloned from it with `CREATE DATABASE ... TEMPLATE`. That is far cheaper
than re-running the migrations and fixture per app, and it keeps tests isolated
while running in parallel against a shared server. The clone is dropped on
`app.Cleanup()`.

The **template** databases are not dropped: Go gives a test binary no reliable
exit hook, so each `go test ./...` leaves one template per package that uses
`tests` (~8, a few MB each). Clones from a killed run (SIGKILL, a panic in a
test binary) can survive too. Neither affects correctness — a later run builds
its own template under its own pid — but they waste disk and slow the server, so
purge them when convenient:

```bash
docker-compose exec postgres-test psql -U pocketbase -d postgres -tAc "select 'DROP DATABASE IF EXISTS \"'||datname||'\" WITH (FORCE);' from pg_database where datname like 'pb\_test%'" | docker-compose exec -T postgres-test psql -U pocketbase -d postgres
```

Seed data lives in `tests/data/fixture.json` and is applied through the regular
collection sync, so the tests exercise the same DDL path as the application.

## Behaviour differences from upstream SQLite PocketBase

These are intentional and user visible.

### Filter API

- **`~` (like) stays case-insensitive.** It compiles to `ILIKE`. Under SQLite
  this came free from `LIKE`; plain Postgres `LIKE` is case-sensitive, so the
  operator would silently have changed meaning.
- **`strftime()` is emulated, not passed through.** The format string is
  translated to a `to_char` template at query build time. Supported
  substitutions: `%Y %m %d %H %M %S %j %f %p %I` and `%%`. Anything else, and any
  SQLite date *modifier* argument (`'+1 day'`, `'start of month'`), returns an
  explicit error rather than silently producing different data.
- **`@rowid` still works.** Every record table carries a real `_rowid_ BIGSERIAL`
  column, so insertion-order semantics are preserved rather than approximated.

### Values

- **JSON field extraction returns text.** Postgres `#>>` yields text where
  SQLite's `json_extract` returned typed values. Comparing a JSON-stored number
  against a SQL number may behave differently than upstream.
- **Number fields are `DOUBLE PRECISION`**, not `NUMERIC`. PocketBase handles
  these values as `float64`, and pgx decodes `NUMERIC` into a decimal string.

### Case-insensitive indexes

SQLite's `COLLATE NOCASE` has no Postgres equivalent, so unique indexes on
identity fields are emitted as `LOWER(col)` functional indexes and the auth
lookups compare with `LOWER(...)` to match. No extension (`citext`) is required.

### `select *` in view queries

Every record table carries an internal `_rowid_` column (see `@rowid` above), so
a view defined with `select * from sometable` includes it as a physical column.
`TableColumns` and `TableInfo` both hide `_rowid_`, so it never becomes a
collection field and never appears in the API — but it is visible if you inspect
the view in Postgres directly. Select the columns explicitly if you want to
avoid it.

### Superuser SQL console

Raw SQL runs over a separate connection using pgx's *simple* protocol, because
the default extended protocol rejects multi-statement queries. Sending several
statements separated by `;` therefore works, and the console reports the **last**
result set produced. Writes run inside a transaction that is committed only if
every statement succeeds.

### Index predicates on JSON columns

Postgres has no implicit cast between `jsonb` and text, so a partial index whose
`WHERE` clause compares a JSON-backed column to a string literal needs an
explicit cast. This affects `json` fields and any multi-value `relation`,
`select`, or `file` field, since those are stored as `jsonb`.

```sql
-- fails: operator does not exist: jsonb <> unknown
CREATE UNIQUE INDEX idx ON demo4 (rel_many_unique) WHERE rel_many_unique != '[]'

-- works
CREATE UNIQUE INDEX idx ON demo4 (rel_many_unique) WHERE rel_many_unique::text != '[]'
```

SQLite accepted the uncast form, so index definitions carried over from upstream
may need updating.

### Collection ordering

`FindAllCollections` orders by `created ASC, id ASC`. Upstream relied on SQLite's
physical `rowid`. Collections created within the same timestamp tick may order
differently than upstream.

### Backups

`CreateBackup` shells out to `pg_dump` and stores the result as
`pb_database.sql` inside the backup archive alongside `pb_data`. `RestoreBackup`
loads it back with `psql --single-transaction`. Restore remains unsupported on
Windows, as upstream.

**With the bundled compose file this needs no configuration.** If the tools are
not in `PATH` and the `pocketbase-postgres` container is running, PocketBase
runs them inside it automatically. That covers the normal development setup —
Postgres in Docker, nothing but Go on the host — and keeps working after the
routine resets that wipe every other place the setting could live (`pb_data`
deleted, or the database volume recreated by `docker compose down -v`).

To point it somewhere else — a different container, a custom path, a wrapper
script:

```bash
pocketbase backup-tools --pgDump "docker exec -i pocketbase-postgres pg_dump" --psql "docker exec -i pocketbase-postgres psql"
```

Run `pocketbase backup-tools` with no flags to print what currently resolves.

These are stored **in the database**, not in a file under `pb_data`. That matters
for two reasons: backups are usually triggered from the admin UI of an already
running server, long after any env variable could have been set; and `pb_data`
is now disposable — records live in Postgres, so wiping it is a normal reset
that must not silently break backups.

Resolution order: `PB_PG_DUMP` / `PB_PSQL` env variables, then the database,
then `PATH`, then the bundled `pocketbase-postgres` container if it is running.

> The container fallback only applies when the tools are missing from `PATH`.
> If you run your own Postgres *and* happen to have a container by that name,
> set the commands explicitly so backups cannot target the wrong server.

> **Why not the app settings?** The values are executed as host commands, so
> exposing them through `PATCH /api/settings` would let any superuser run
> arbitrary commands on the server. They are kept in their own `_params` row,
> outside the settings payload, so changing them requires access to this CLI —
> the same bar as before.

> Prefer `docker exec -i <container>` over `docker compose exec`: the compose
> form only works when the server's working directory contains the compose
> file, which is rarely true for a deployed binary.

The dump is streamed over stdout/stdin rather than written with `--file`, so the
archive lands on the host even when the tool runs inside a container. Two
caveats: the value is split on spaces (use a wrapper script for paths containing
spaces), and the connection string is resolved *by the tool*, so when it runs in
the container the host and port in `PB_DB_URL` must be reachable from there.

#### Backups do not block writes

Upstream wrapped the backup in a transaction to freeze writes while the SQLite
file inside `pb_data` was copied. The database is no longer in there, so that
pause bought nothing and has been removed.

`pg_dump` needs no coordination of its own — it reads a consistent MVCC
snapshot, so concurrent writes neither affect it nor wait for it.

What does need care is that an archive holds **two** things that must agree: the
SQL dump, taken first, and the files in `pb_data`, archived a moment later. The
skew is only dangerous in one direction:

| Happens between dump and archive | Result |
| --- | --- |
| A record and its file are **created** | File in the archive with no matching record — a harmless orphan |
| A record is **deleted**, or its file **replaced** | The dump still references a file the archive no longer has — a **broken attachment** |

The second case is prevented by **postponing file deletions** while a backup is
running. File deletion here was already asynchronous and best-effort
("optimistic delete", failures logged and ignored), so delaying it fits the
existing contract; the queue is drained as soon as the backup finishes. Nothing
waits on a request path.

Two things still worth knowing:

- Only *PocketBase's* deletions are postponed. Anything else touching that
  storage keeps deleting.
- `pg_dump` does genuinely block **DDL**: it holds `ACCESS SHARE` on every
  table, so a collection schema change started during a backup waits for it,
  and a backup started during one waits in turn.

### View collections

View queries are stored SQL authored against the database. Any that fail to
resolve are reported **at startup**, listing every broken view at once, rather
than surfacing later as opaque runtime API errors.

## Migrations

The SQLite upgrade chain (the `v0.23` migrations and the index normalization
migration) was removed. This fork initializes a fresh Postgres schema and has no
path for importing an existing SQLite `data.db`.

## Notes for contributors

Two Postgres behaviours cause most porting bugs here:

1. **A failed statement aborts the whole transaction.** Any "best effort" query
   whose error is logged and ignored will poison every later statement in the
   same transaction (`SQLSTATE 25P02`). Upstream had several of these as SQLite
   pragmas.
2. **pgx uses the extended protocol**, which rejects multiple commands in one
   `Execute()`. Split multi-statement DDL into separate calls.
