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

# An encryption key is optional - upstream PocketBase does not encrypt settings
# by default either. Only pass the flag when a key is actually present,
# otherwise PocketBase would look for a key that is not there.
if [ -n "${PB_ENCRYPTION_KEY:-}" ]; then
    exec /usr/local/bin/pocketbase "$@" --dir=/pb_data --encryptionEnv=PB_ENCRYPTION_KEY
fi

exec /usr/local/bin/pocketbase "$@" --dir=/pb_data
