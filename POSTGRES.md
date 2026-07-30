# PocketBase on Postgres

This fork replaces SQLite with PostgreSQL. There is no SQLite code path left and
no build tag to switch back.

## Requirements

- Go 1.24+
- PostgreSQL 16 or newer (the `IS JSON` predicate is used); the bundled compose
  file pins **18**
- `pg_dump` / `psql` in `PATH`, but only if you use the backup feature

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
3. the built-in default above

```bash
go run ./examples/base serve --dbUrl "postgres://user:pass@db.example.com:5432/pocketbase?sslmode=require"
```

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
loads it back with `psql --single-transaction`. Both require the respective
binary in `PATH`. Restore remains unsupported on Windows, as upstream.

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
