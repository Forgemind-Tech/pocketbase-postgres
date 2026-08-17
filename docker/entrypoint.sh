#!/bin/sh
# Entrypoint for the container image.
#
# Applies the flags every invocation needs, so that "docker compose exec"
# works the same as the running server. Without this a bare
# "pocketbase superuser ..." runs without --dir and --encryptionEnv and fails
# with a confusing "missing encryption key" - the settings were written
# encrypted by the serving process.
set -e

# The values shipped in .env.example. Starting with them means the deployment
# was never configured, which is worth failing loudly about rather than
# quietly running a public service on documented credentials.
EXAMPLE_KEY='change-me-32-characters-long-000'
EXAMPLE_DB_PASSWORD='change-me-please'

if [ "${PB_ENCRYPTION_KEY:-}" = "$EXAMPLE_KEY" ]; then
    echo "refusing to start: PB_ENCRYPTION_KEY is still the example value from .env.example" >&2
    echo "generate one with:  openssl rand -base64 24 | cut -c1-32" >&2
    exit 1
fi

case "${PB_DB_URL:-}" in
    *":$EXAMPLE_DB_PASSWORD@"*)
        echo "refusing to start: POSTGRES_PASSWORD is still the example value from .env.example" >&2
        echo "generate one with:  openssl rand -base64 24" >&2
        exit 1
        ;;
esac

# Every directory PocketBase reads or writes is named explicitly, and each one
# is a separate mount point so a deployment can bind-mount it from the host.
#
# The defaults would not do: pb_hooks and pb_migrations are resolved relative to
# the data dir ("pb_data/../pb_hooks"), and pb_public relative to the
# *executable* - which here would be /usr/local/bin/pb_public. All three would
# land on the image's ephemeral layer, so hooks could not be added from the host
# and anything written there would be lost on the next update.
PB_DIRS='--dir=/pb_data --hooksDir=/pb_hooks --migrationsDir=/pb_migrations --publicDir=/pb_public'

# An encryption key is optional - upstream PocketBase does not encrypt settings
# by default either. Only pass the flag when a key is actually present,
# otherwise PocketBase would look for a key that is not there.
if [ -n "${PB_ENCRYPTION_KEY:-}" ]; then
    exec /usr/local/bin/pocketbase "$@" $PB_DIRS --encryptionEnv=PB_ENCRYPTION_KEY
fi

exec /usr/local/bin/pocketbase "$@" $PB_DIRS
