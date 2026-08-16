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
# A minimal Alpine image with only the Postgres *client* tools copied in, at
# exactly the server's major version - the backup feature shells out to
# pg_dump/psql, and pg_dump refuses to dump a server newer than itself.
#
# Basing directly on postgres:18-alpine would guarantee that parity too, but it
# drags in the Postgres server, a compiler and gosu (a Go binary whose older
# stdlib carries critical CVEs) for a 476MB image. Copying the two binaries and
# libpq gives the same parity in ~30MB, with none of that attack surface.
# Deleting those files afterwards would not have helped: a file removed in a
# later layer still exists in the image.
FROM alpine:3.24

RUN apk add --no-cache         krb5-libs openldap libsasl libedit zstd-libs lz4-libs         openssl zlib ncurses-libs         ca-certificates         tzdata

# client tools and libpq, taken from the matching Postgres image
COPY --from=postgres:18-alpine /usr/local/bin/pg_dump /usr/local/bin/pg_dump
COPY --from=postgres:18-alpine /usr/local/bin/psql    /usr/local/bin/psql
COPY --from=postgres:18-alpine /usr/local/lib/libpq.so.5 /usr/local/lib/libpq.so.5

# the app never runs as root; the data dir is created owned by it so that a
# fresh named volume inherits the right ownership
RUN addgroup -S pb && adduser -S -G pb pb     && mkdir -p /pb_data && chown pb:pb /pb_data

COPY --from=build /out/pocketbase /usr/local/bin/pocketbase

# the shim applies the flags every invocation needs and refuses to start on the
# example credentials; see docker/entrypoint.sh
COPY docker/entrypoint.sh /usr/local/bin/pb
RUN chmod +x /usr/local/bin/pb

USER pb
WORKDIR /pb_data
EXPOSE 8090

# ENTRYPOINT is the shim rather than the binary, so that "docker compose exec
# pocketbase pb ..." and the server itself get identical flags
ENTRYPOINT ["/usr/local/bin/pb"]
CMD ["serve", "--http=0.0.0.0:8090"]
