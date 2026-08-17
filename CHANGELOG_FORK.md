# Fork changelog

Changes specific to this PostgreSQL fork, and a record of what was taken from
each upstream release.

`CHANGELOG.md` is kept **byte-identical to upstream** so that pulling upstream
changes never conflicts there — fork notes belong here instead.

See [POSTGRES.md](POSTGRES.md) for how the port actually works.

---

## Upstream merges

### v0.39.11 — partially merged

Upstream changed **no Go source at all** in this release; it is entirely UI,
dependency bumps and changelog.

**Taken**

- `ui/**`: "API preview" example fixes, sortable `dragend` handling, ESC to
  escape the TAB trap on rule fields, duplicated collections can edit the
  target of their relation fields, shablon update. `ui/dist` matches upstream
  v0.39.11 exactly.
- `go.mod` / `go.sum`: `golang.org/x/crypto` v0.55.0, `x/net` v0.58.0,
  `x/mod` v0.40.0, `x/tools` v0.49.0. The `golang.org/x/*` block now matches
  upstream v0.39.11 exactly — `x/image` v0.45.0 and `x/text` v0.41.0 were
  already here, bumped ahead of upstream to clear a container-scan CVE.
- `.github/workflows/release.yaml`: min Go for `actions/setup-go`
  1.26.5 → 1.26.6, for the Go 1.26.6 security fixes.

**Deliberately not taken**

- `modernc_versions_check.go`: deleted by the port.
- `CHANGELOG_16_22.md`: not present in this fork.

**Note on the merge itself**

`git merge v0.39.11` is not usable here. v0.39.10 was applied as a
cherry-picked diff rather than a merge commit, so `git merge-base` resolves
back past it and git re-proposes the whole v0.39.9..v0.39.11 span — 23
conflicts, most of them rename/rename on `ui/dist` bundles whose content
hashes changed on both sides. Apply the `v0.39.10..v0.39.11` diff per path
instead, as below.

### v0.39.10 — partially merged

Cherry-picked rather than merged wholesale.

**Taken**

- `pocketbase.go`: replaced the two `routine.FireAndForget` shutdown goroutines
  with plain `go func()`. The then-unused `tools/routine` import was dropped —
  upstream still needs it for the modernc SQLite deps check, which this fork
  does not have, so the patch applied cleanly but did not compile until the
  import was removed.
- `core/field_file.go`: guard against nil `*filesystem.File` values in
  `toSliceValue`, plus the accompanying test cases.
- `ui/**`: dashboard updates (logs chart and list). `ui/dist` matches upstream
  v0.39.10 exactly.

**Deliberately not taken**

- `go.mod` / `go.sum`: `modernc.org/sqlite` v1.54 → v1.55. This fork has no
  SQLite dependency and the bump would reintroduce one.
- `modernc_versions_check.go`: deleted by the port.
- `CHANGELOG_16_22.md`: not present in this fork.

---

## Fork changes

### Connection pools

Reads and writes use separate pools, sized as a set to fit a stock Postgres
(`max_connections = 100`): 40 data read, 20 data write, 8 aux read, 4 aux write.

The write pool was previously capped at a **single** connection — a SQLite
workaround, since SQLite allows only one writer. On Postgres it served only to
serialise every write in the application. Lifting it took a benchmark of 100
concurrent 20 ms writes from **2.28 s to 142 ms**.

PocketBase now warns at startup if the four pools together exceed what the
server accepts; the previous defaults could open 142 connections against a
100-connection server.

### Backups

- Run `pg_dump` / `psql` instead of copying a database file. The tools do not
  have to be installed on the host: the commands are resolved from
  `PB_PG_DUMP` / `PB_PSQL`, then the database, then `PATH`, then the bundled
  Postgres container.
- Backups **do not block writes**. `pg_dump` reads a consistent MVCC snapshot,
  and file deletions are postponed while a backup runs so the archived files
  cannot lose something the dump still references.

### Connection configuration

Resolves from `--dbUrl`, then `PB_DB_URL`, then `db.json` in the data directory,
then built-in defaults matching the bundled compose file. `db show` / `db set` /
`db test` manage the stored settings and work even when the database is
unreachable, since that is when they are needed.

### Container directory layout

The bundle mounts `pb_data`, `pb_hooks`, `pb_migrations` and `pb_public` as plain
directories next to `compose.yaml`, and runs the app as the host user that owns
them (`PB_UID`/`PB_GID` in `.env`).

Before this, only `pb_data` was mounted, as a named volume, and the other three
did not exist in the image at all. Their defaults are resolved relative to the
data dir (`pb_data/../pb_hooks`) or, for `pb_public`, to the *executable* — which
in the image meant `/usr/local/bin/pb_public`. All three landed on the container's
ephemeral layer, so **the JS hook system was silently unusable**: a missing hooks
directory is not an error (`filesContent` returns an empty map on
`fs.ErrNotExist`), so the plugin loaded, found nothing, and logged nothing.
Anything written there was lost on the next update.

`docker/entrypoint.sh` now names all four directories explicitly rather than
relying on that path arithmetic.

Running as the host user is what makes bind mounts workable on Linux, where a
directory owned by uid 1000 is not writable by the image's `pb` user (101).
`install.sh` derives the ids from the host: the invoking user normally; the
image's own user when installing as root, so the app never runs as root inside
the container; and the same fallback when `id` gives nothing usable, as in Git
Bash on Windows, where the reported uid is derived from a Windows SID. Docker
Desktop on macOS and Windows presents bind mounts as owned by whoever asks, which
is precisely why this is decided from the host rather than by trying it — a wrong
value works there and fails on a server.

`install.sh --update` detects an install still using the named volume and offers
to copy it into `./pb_data` (via `docker cp`, so it works on Windows and the
files land owned by the invoking user). The volume is left in place afterwards.

The database keeps its named volume. Postgres owns that directory's layout and
permissions, there is nothing in it to edit by hand, and bind-mounting it only
invites permission trouble.

One consequence worth knowing: `pb_hooks` is executable code loaded into the
server process, so write access to that directory is equivalent to code execution
inside the container. The container limits still apply, but the hardening no
longer rests on the app's code being fixed at image build time.

### The port itself

Replaced SQLite with PostgreSQL (`pgx/v5`). Data lives in the `public` schema
and logs in `pb_aux`, since Postgres cannot attach a second file the way SQLite
does. Retries are keyed on SQLSTATE, introspection runs against the Postgres
catalogs, and view collections are validated at startup rather than failing
later at runtime.

**Deliberate API breaks:** `strftime` is translated to `to_char` and errors on
unsupported substitutions rather than silently returning different data;
`@rowid` is backed by a real `BIGSERIAL` column; `LIKE` becomes `ILIKE`;
`COLLATE NOCASE` becomes `LOWER()` functional indexes; JSON extraction returns
text.

Removed the SQLite-era migrations (`v0.23_migrate*`, `normalize_indexes`) and
the pure-Go SQLite driver version check, since neither can apply here.
