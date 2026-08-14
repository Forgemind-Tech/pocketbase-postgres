# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# cached separately so source edits do not re-download the module graph
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# the version is stamped in, same as the Makefile targets
ARG VERSION=dev

# CGO is off: the whole stack including the pgx driver is pure Go, so the
# binary is static and the runtime layer needs no Go toolchain
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/pocketbase/pocketbase.Version=${VERSION}" \
    -o /out/pocketbase ./examples/base

# ---- runtime --------------------------------------------------------------
#
# Based on the Postgres image purely to obtain pg_dump/psql at *exactly* the
# server's major version - the backup feature shells out to them, and pg_dump
# refuses to dump a server newer than itself.
#
# The alternative, installing a client package on plain Alpine, does not work
# here: stable Alpine currently tops out at postgresql17-client, and pinning to
# the rolling "edge" repository is not something to do in a deployed image.
#
# The Postgres server binaries come along unused. If image size ever matters
# more than this guarantee, copy pg_dump/psql plus their libpq dependencies out
# of this image instead - but then the version parity has to be checked by hand.
FROM postgres:18-alpine

# the app never runs as root; the data dir is created owned by it so that a
# fresh named volume inherits the right ownership
RUN addgroup -S pb && adduser -S -G pb pb \
    && mkdir -p /pb_data && chown pb:pb /pb_data

COPY --from=build /out/pocketbase /usr/local/bin/pocketbase

# "docker compose exec" bypasses CMD, so a bare "pocketbase superuser ..." runs
# without --dir and --encryptionEnv and fails with a confusing
# "missing encryption key" - the settings were written encrypted by the serving
# process. This shim applies the same flags, so CLI commands work:
#
#   docker compose exec pocketbase pb superuser upsert EMAIL PASS
RUN printf '%s\n' \
    '#!/bin/sh' \
    '# runs the pocketbase binary with this image'"'"'s standard flags' \
    'set -e' \
    'if [ -n "${PB_ENCRYPTION_KEY:-}" ]; then' \
    '  exec /usr/local/bin/pocketbase "$@" --dir=/pb_data --encryptionEnv=PB_ENCRYPTION_KEY' \
    'fi' \
    'exec /usr/local/bin/pocketbase "$@" --dir=/pb_data' \
    > /usr/local/bin/pb \
    && chmod +x /usr/local/bin/pb

USER pb
WORKDIR /pb_data
EXPOSE 8090

# override the Postgres image's entrypoint - this container runs PocketBase
ENTRYPOINT ["/usr/local/bin/pocketbase"]
CMD ["serve", "--http=0.0.0.0:8090", "--dir=/pb_data"]
