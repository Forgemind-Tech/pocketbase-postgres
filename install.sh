#!/bin/sh
# PocketBase on PostgreSQL - installer and updater.
#
# Writes a compose file and a .env, starts the stack, and creates the first
# superuser. Downloads nothing else.
#
#   curl -fsSL https://raw.githubusercontent.com/Forgemind-Tech/pocketbase-postgres/master/install.sh -o install.sh
#   sh install.sh
#
# Read it before running it - that is why it is a download rather than a pipe
# into a shell.
#
# Non-interactive (for scripts and CI):
#
#   sh install.sh --yes --port 8090 --dir ./pocketbase
#
# Updating an existing install (run it from the install directory, or pass --dir):
#
#   sh install.sh --update              # newest image for the configured tag
#   sh install.sh --update --tag v1.2.3 # switch to a specific release
#
# Changing resource limits later (from the install directory):
#
#   sh install.sh --resources                    # pick a size interactively
#   sh install.sh --resources --profile large    # or set one directly
#   sh install.sh --resources --pg-mem 4g --pg-cpu 2
#
# Options:
#   --dir PATH        where to install            (default ./pocketbase)
#   --port N          host port for the dashboard (default 8090)
#   --db-user NAME    database user               (default pocketbase)
#   --db-name NAME    database name               (default pocketbase)
#   --db-pass SECRET  database password           (default: generated)
#   --tag TAG         image tag to deploy         (default latest)
#   --tz ZONE         timezone, eg. Africa/Dar_es_Salaam (default: detected)
#   --admin-email X   first superuser email
#   --admin-pass X    first superuser password    (at least 8 characters)
#   --profile NAME    resource sizing: small|medium|large|xlarge|none
#                     (default: chosen from the host's memory)
#   --pb-mem SIZE     app memory cap,      eg. 512m, 2g, 0 for unlimited
#   --pb-cpu N        app cpu cap,         eg. 0.5, 2, 0 for unlimited
#   --pg-mem SIZE     database memory cap
#   --pg-cpu N        database cpu cap
#   --resources       change limits on an existing install, then restart it
#   --update          update an existing install instead of creating one
#   --yes             accept defaults, never prompt
set -eu

REPO_IMAGE="${PB_IMAGE_REPO:-ghcr.io/forgemind-tech/pocketbase-postgres}"
MIN_PASSWORD_LEN=8

INSTALL_DIR="./pocketbase"
PB_PORT="8090"
DB_USER="pocketbase"
DB_NAME="pocketbase"
DB_PASS=""
IMAGE_TAG="latest"
ADMIN_EMAIL=""
ADMIN_PASS=""
ASSUME_YES="0"
DO_UPDATE="0"
TZ_NAME=""
TAG_GIVEN="0"
TZ_UNVERIFIED=0
DO_RESOURCES="0"
PROFILE=""
PB_MEM=""
PB_CPU=""
PG_MEM=""
PG_CPU=""

die() { printf 'error: %s\n' "$1" >&2; exit 1; }
say() { printf '%s\n' "$1"; }

while [ $# -gt 0 ]; do
    case "$1" in
        --dir) INSTALL_DIR="${2:-}"; shift 2 ;;
        --port) PB_PORT="${2:-}"; shift 2 ;;
        --db-user) DB_USER="${2:-}"; shift 2 ;;
        --db-name) DB_NAME="${2:-}"; shift 2 ;;
        --db-pass) DB_PASS="${2:-}"; shift 2 ;;
        --tag) IMAGE_TAG="${2:-}"; TAG_GIVEN="1"; shift 2 ;;
        --tz) TZ_NAME="${2:-}"; shift 2 ;;
        --admin-email) ADMIN_EMAIL="${2:-}"; shift 2 ;;
        --admin-pass) ADMIN_PASS="${2:-}"; shift 2 ;;
        --profile) PROFILE="${2:-}"; shift 2 ;;
        --pb-mem) PB_MEM="${2:-}"; shift 2 ;;
        --pb-cpu) PB_CPU="${2:-}"; shift 2 ;;
        --pg-mem) PG_MEM="${2:-}"; shift 2 ;;
        --pg-cpu) PG_CPU="${2:-}"; shift 2 ;;
        --resources) DO_RESOURCES="1"; shift ;;
        --update) DO_UPDATE="1"; shift ;;
        --yes|-y) ASSUME_YES="1"; shift ;;
        -h|--help) sed -n '2,50p' "$0"; exit 0 ;;
        *) die "unknown option: $1 (try --help)" ;;
    esac
done

# ---------------------------------------------------------------- checks ----
command -v docker >/dev/null 2>&1 || die "docker is not installed - see https://docs.docker.com/get-docker/"

if docker compose version >/dev/null 2>&1; then
    COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE="docker-compose"
else
    die "docker compose is not available - install the Compose plugin"
fi

docker info >/dev/null 2>&1 || die "cannot talk to the docker daemon - is it running, and is your user in the 'docker' group?"

# ------------------------------------------------------------ validators ----
valid_port() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ "$1" -ge 1 ] 2>/dev/null && [ "$1" -le 65535 ] 2>/dev/null
}

# postgres identifiers: letters, digits and underscore, not starting with a digit
valid_ident() {
    case "$1" in
        ''|*[!A-Za-z0-9_]*) return 1 ;;
        [0-9]*) return 1 ;;
    esac
    return 0
}

valid_email() {
    case "$1" in
        *[!\ ]*@*.*) return 0 ;;
        *) return 1 ;;
    esac
}

valid_password() {
    [ "${#1}" -ge "$MIN_PASSWORD_LEN" ]
}

valid_tz() {
    [ -n "$1" ] || return 1
    [ "$1" = "UTC" ] && return 0
    if [ -d /usr/share/zoneinfo ]; then
        [ -e "/usr/share/zoneinfo/$1" ] || return 1
        return 0
    fi
    # No tz database on this host (eg. Git Bash on Windows). Ask the postgres
    # image instead - it ships tzdata and is pulled anyway, so this checks the
    # name against the same database the containers will use.
    if docker image inspect postgres:18 >/dev/null 2>&1; then
        docker run --rm --entrypoint test postgres:18 -f "/usr/share/zoneinfo/$1" && return 0
        return 1
    fi
    # nothing available to check against: require Area/City and warn later
    case "$1" in
        */*) TZ_UNVERIFIED=1; return 0 ;;
        *) return 1 ;;
    esac
}

valid_tag() {
    case "$1" in ''|*[!A-Za-z0-9._-]*) return 1 ;; esac
    return 0
}

detect_tz() {
    if [ -n "${TZ:-}" ]; then printf '%s\n' "$TZ"; return; fi
    if [ -f /etc/timezone ]; then sed -n '1p' /etc/timezone; return; fi
    if [ -L /etc/localtime ]; then
        readlink /etc/localtime | sed 's|.*/zoneinfo/||'
        return
    fi
    printf 'UTC\n'
}

# --------------------------------------------------------------- prompts ----
interactive() {
    [ "$ASSUME_YES" != "1" ] && [ -r /dev/tty ]
}

ask() { # ask VAR "prompt" "default"
    _def="$3"
    if ! interactive; then
        eval "$1=\"\$_def\""
        return
    fi
    printf '%s [%s]: ' "$2" "$_def" > /dev/tty
    read -r _ans < /dev/tty || _ans=""
    [ -n "$_ans" ] || _ans="$_def"
    eval "$1=\"\$_ans\""
}

# ask and keep asking until the answer passes; in non-interactive mode a bad
# value is a hard error rather than an infinite loop
ask_valid() { # ask_valid VAR "prompt" "default" validator "error text"
    while :; do
        ask "$1" "$2" "$3"
        eval "_val=\"\$$1\""
        if "$4" "$_val"; then
            return 0
        fi
        if ! interactive; then
            die "$5 (got '$_val')"
        fi
        printf '  %s\n' "$5" > /dev/tty
    done
}

generate_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -base64 24 | tr -d '/+=' | cut -c1-24
    else
        tr -dc 'A-Za-z0-9' < /dev/urandom 2>/dev/null | head -c 24
    fi
}

wait_for_health() { # wait_for_health PORT
    printf 'waiting for the server'
    _i=0
    while [ "$_i" -lt 60 ]; do
        if curl -fsS -m 3 "http://127.0.0.1:$1/api/health" >/dev/null 2>&1; then
            say " ok"
            return 0
        fi
        printf '.'
        _i=$((_i + 1))
        sleep 2
    done
    say ""
    return 1
}


# ------------------------------------------------------- resource sizing ----
# Limits are caps, not reservations. The right numbers depend entirely on the
# host and the workload, so rather than hardcoding one answer we size from what
# Docker reports and let the user pick or override.
valid_mem() {
    _v="$1"
    [ -n "$_v" ] || return 1
    case "$_v" in *[bBkKmMgG]) _v="${_v%?}" ;; esac
    case "$_v" in ''|*[!0-9]*) return 1 ;; esac
    return 0
}

valid_cpu() {
    case "$1" in
        ''|*[!0-9.]*) return 1 ;;
        *.*.*) return 1 ;;
        .*|*.) return 1 ;;
    esac
    return 0
}

host_mem_bytes() { docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0; }
host_cpus()      { docker info --format '{{.NCPU}}' 2>/dev/null || echo 0; }

# profile_values NAME -> "PB_MEM PB_CPU PG_MEM PG_CPU"
profile_values() {
    case "$1" in
        small)  echo "256m 0.5 512m 0.5" ;;
        medium) echo "512m 1.0 1g 1.0" ;;
        large)  echo "1g 2.0 2g 2.0" ;;
        xlarge) echo "2g 4.0 4g 4.0" ;;
        none)   echo "0 0 0 0" ;;
        *)      return 1 ;;
    esac
}

# pick a profile that fits the host rather than assuming a server size
suggest_profile() {
    _mb=$(( $(host_mem_bytes) / 1048576 ))
    if   [ "$_mb" -le 0 ];    then echo medium
    elif [ "$_mb" -lt 2048 ]; then echo small
    elif [ "$_mb" -lt 4096 ]; then echo medium
    elif [ "$_mb" -lt 8192 ]; then echo large
    else                           echo xlarge
    fi
}

apply_profile() { # apply_profile NAME - fills any value not set explicitly
    set -- $(profile_values "$1")
    [ -n "$PB_MEM" ] || PB_MEM="$1"
    [ -n "$PB_CPU" ] || PB_CPU="$2"
    [ -n "$PG_MEM" ] || PG_MEM="$3"
    [ -n "$PG_CPU" ] || PG_CPU="$4"
}

choose_resources() {
    _suggested="$(suggest_profile)"
    _mb=$(( $(host_mem_bytes) / 1048576 ))
    _cpus="$(host_cpus)"

    if [ -n "$PROFILE" ]; then
        profile_values "$PROFILE" >/dev/null || die "unknown profile '$PROFILE' (small|medium|large|xlarge|none)"
        apply_profile "$PROFILE"
    elif interactive; then
        say ""
        say "This host offers ${_mb}MB of memory and ${_cpus} CPUs to Docker."
        say ""
        say "Resource limits are caps, not reservations - they stop a runaway"
        say "container taking the host down. Too low and the kernel kills the"
        say "process, so pick for the workload you expect."
        say ""
        say "  small   app 256m / db 512m    a small site or a 1GB VPS"
        say "  medium  app 512m / db 1g      a typical app"
        say "  large   app 1g   / db 2g      a busy app"
        say "  xlarge  app 2g   / db 4g      a heavily used deployment"
        say "  none    no limits             let it use whatever it needs"
        say "  custom  enter exact values"
        say ""
        while :; do
            ask _prof "Size" "$_suggested"
            case "$_prof" in
                small|medium|large|xlarge|none) apply_profile "$_prof"; break ;;
                custom)
                    ask_valid PB_MEM "  app memory cap (0 = unlimited)" "512m" valid_mem "use a size like 512m, 2g or 0"
                    ask_valid PB_CPU "  app cpu cap (0 = unlimited)" "1.0" valid_cpu "use a number like 0.5, 2 or 0"
                    ask_valid PG_MEM "  database memory cap" "1g" valid_mem "use a size like 512m, 2g or 0"
                    ask_valid PG_CPU "  database cpu cap" "1.0" valid_cpu "use a number like 0.5, 2 or 0"
                    break ;;
                *) printf '  choose small, medium, large, xlarge, none or custom
' > /dev/tty ;;
            esac
        done
    else
        apply_profile "$_suggested"
    fi

    valid_mem "$PB_MEM" || die "invalid app memory cap '$PB_MEM'"
    valid_cpu "$PB_CPU" || die "invalid app cpu cap '$PB_CPU'"
    valid_mem "$PG_MEM" || die "invalid database memory cap '$PG_MEM'"
    valid_cpu "$PG_CPU" || die "invalid database cpu cap '$PG_CPU'"
}

# update a key in .env, appending it when an older install lacks it
set_env_var() {
    if grep -q "^$1=" .env 2>/dev/null; then
        _t=".env.tmp.$$"
        sed "s|^$1=.*|$1=$2|" .env > "$_t" && mv "$_t" .env
    else
        printf '%s=%s
' "$1" "$2" >> .env
    fi
}

# ------------------------------------------------------------- resources ----
if [ "$DO_RESOURCES" = "1" ]; then
    if [ -f compose.yaml ]; then
        :
    elif [ -f "$INSTALL_DIR/compose.yaml" ]; then
        cd "$INSTALL_DIR"
    else
        die "no compose.yaml here or in $INSTALL_DIR - run this from your install directory, or pass --dir"
    fi
    [ -f .env ] || die "no .env next to compose.yaml"

    say "current limits in $(pwd):"
    for _k in PB_MEM_LIMIT PB_CPU_LIMIT PG_MEM_LIMIT PG_CPU_LIMIT; do
        _cur="$(sed -n "s/^$_k=//p" .env | sed -n '1p')"
        printf '  %-14s %s
' "$_k" "${_cur:-(unset - using the compose default)}"
    done

    choose_resources

    set_env_var PB_MEM_LIMIT "$PB_MEM"
    set_env_var PB_CPU_LIMIT "$PB_CPU"
    set_env_var PG_MEM_LIMIT "$PG_MEM"
    set_env_var PG_CPU_LIMIT "$PG_CPU"

    say ""
    say "new limits: app ${PB_MEM}/${PB_CPU}cpu, database ${PG_MEM}/${PG_CPU}cpu"
    say "restarting so they take effect..."
    $COMPOSE up -d

    _port="$(sed -n 's/^PB_PORT=//p' .env | sed -n '1p')"
    [ -n "$_port" ] || _port="8090"
    if wait_for_health "$_port"; then
        say "done."
    else
        say "the server did not come back healthy - the limits may be too low."
        say "check: $COMPOSE logs"
        exit 1
    fi
    exit 0
fi

# ---------------------------------------------------------------- update ----
if [ "$DO_UPDATE" = "1" ]; then
    if [ -f compose.yaml ]; then
        : # already in an install directory
    elif [ -f "$INSTALL_DIR/compose.yaml" ]; then
        cd "$INSTALL_DIR"
    else
        die "no compose.yaml here or in $INSTALL_DIR - run this from your install directory, or pass --dir"
    fi

    [ -f .env ] || die "no .env next to compose.yaml - cannot tell which image to update"

    say "updating the install in $(pwd)"
    say ""
    say "Back up first if this matters: an update can carry database migrations,"
    say "which apply automatically on the next start."
    if interactive; then
        ask _go "Continue? (y/N)" "N"
        case "$_go" in y|Y|yes|YES) : ;; *) die "cancelled" ;; esac
    fi

    if [ "$TAG_GIVEN" = "1" ]; then
        valid_tag "$IMAGE_TAG" || die "invalid image tag '$IMAGE_TAG'"
        # rewrite only the image line, leaving the rest of .env untouched
        _tmp=".env.tmp.$$"
        sed "s|^PB_IMAGE=.*|PB_IMAGE=$REPO_IMAGE:$IMAGE_TAG|" .env > "$_tmp" && mv "$_tmp" .env
        say "switched to $REPO_IMAGE:$IMAGE_TAG"
    fi

    _port="$(sed -n 's/^PB_PORT=//p' .env | sed -n '1p')"
    [ -n "$_port" ] || _port="8090"

    $COMPOSE pull
    $COMPOSE up -d

    if wait_for_health "$_port"; then
        say ""
        say "updated. Running: $(sed -n 's/^PB_IMAGE=//p' .env | sed -n '1p')"
    else
        say "the server did not come back healthy. Check: $COMPOSE logs"
        exit 1
    fi
    exit 0
fi

# --------------------------------------------------------------- install ----
say "PocketBase on PostgreSQL - installer"
say ""

# A timezone the user asked for must be honoured or rejected - never silently
# swapped for something else. Only a *detected* value falls back quietly.
if [ -n "$TZ_NAME" ]; then
    if ! valid_tz "$TZ_NAME"; then
        interactive || die "invalid timezone '$TZ_NAME' - use a zone name like Africa/Dar_es_Salaam, Europe/London or UTC"
        say "invalid timezone '$TZ_NAME' - pick another below"
        TZ_NAME="UTC"
    fi
else
    TZ_NAME="$(detect_tz)"
    valid_tz "$TZ_NAME" || TZ_NAME="UTC"
fi

if interactive; then
    ask       INSTALL_DIR "Install directory" "$INSTALL_DIR"
    ask_valid PB_PORT     "Port for the dashboard and API" "$PB_PORT" \
              valid_port "port must be a number between 1 and 65535"
    ask_valid DB_USER     "Database user" "$DB_USER" \
              valid_ident "use letters, digits and underscore, not starting with a digit"
    ask_valid DB_NAME     "Database name" "$DB_NAME" \
              valid_ident "use letters, digits and underscore, not starting with a digit"
    ask_valid TZ_NAME     "Timezone" "$TZ_NAME" \
              valid_tz "use a zone name like Africa/Dar_es_Salaam, Europe/London or UTC"
    ask_valid IMAGE_TAG   "Image tag" "$IMAGE_TAG" \
              valid_tag "tags may contain letters, digits, dot, dash and underscore"
else
    valid_port "$PB_PORT"   || die "port must be a number between 1 and 65535 (got '$PB_PORT')"
    valid_ident "$DB_USER"  || die "invalid database user '$DB_USER'"
    valid_ident "$DB_NAME"  || die "invalid database name '$DB_NAME'"
    valid_tag "$IMAGE_TAG"  || die "invalid image tag '$IMAGE_TAG'"
    valid_tz "$TZ_NAME"     || die "invalid timezone '$TZ_NAME'"
fi

# check the superuser details now rather than after the stack is up - failing
# on a too-short password only once everything has started is a poor trade
if [ -n "$ADMIN_EMAIL" ]; then
    valid_email "$ADMIN_EMAIL" || die "invalid superuser email '$ADMIN_EMAIL'"
fi
if [ -n "$ADMIN_PASS" ]; then
    valid_password "$ADMIN_PASS" || die "superuser password must be at least $MIN_PASSWORD_LEN characters"
fi

choose_resources

[ -n "$DB_PASS" ] || DB_PASS="$(generate_secret)"
[ -n "$DB_PASS" ] || die "could not generate a database password - pass --db-pass"

mkdir -p "$INSTALL_DIR" || die "cannot create $INSTALL_DIR"
cd "$INSTALL_DIR" || die "cannot enter $INSTALL_DIR"

if [ -f compose.yaml ] || [ -f .env ]; then
    say ""
    say "An install already exists in $(pwd)."
    say "To update it instead, run:  sh install.sh --update"
    ask _overwrite "Overwrite compose.yaml and .env? (y/N)" "N"
    case "$_overwrite" in
        y|Y|yes|YES) : ;;
        *) die "left the existing install untouched" ;;
    esac
fi

# ------------------------------------------------------------- write files --
cat > compose.yaml <<'COMPOSE_EOF'
# PocketBase on PostgreSQL. Generated by install.sh.
#
#   docker compose up -d          start
#   docker compose logs -f        follow the logs
#   docker compose down           stop (keeps your data)
#   docker compose down -v        stop and DELETE all data
#
# Settings live in .env next to this file.
#
# note: no "name:" here on purpose - Compose then uses this directory's name as
# the project name, so separate installs on one host stay separate.

services:
  postgres:
    image: postgres:18@sha256:06cad38a5d9f5d24b4d83d86def30795d5e4b757fedbf5281172b576dedcd941
    restart: unless-stopped
    environment:
      POSTGRES_USER: ${POSTGRES_USER:?}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?}
      POSTGRES_DB: ${POSTGRES_DB:?}
      TZ: ${TZ:-UTC}
    # Caps, not reservations: they stop a runaway container taking the host
    # down, they do not reserve anything. Set them too low and the kernel will
    # OOM-kill the database, so tune in .env rather than trimming blindly.
    deploy:
      resources:
        limits:
          cpus: "${PG_CPU_LIMIT:-1.0}"
          memory: ${PG_MEM_LIMIT:-1g}
          pids: 512
    # hardening: Postgres needs CHOWN/SETUID/SETGID/DAC_OVERRIDE/FOWNER to
    # initialise its data directory and drop to the postgres user
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETGID
      - SETUID
    volumes:
      # postgres:18+ expects a single mount here and puts the cluster in a
      # major-version subdirectory beneath it
      - pgdata:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 20
    # not published to the host - only the app reaches it, over this network

  pocketbase:
    image: ${PB_IMAGE:?}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      PB_DB_URL: postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
      # affects log timestamps and cron schedules (eg. automatic backups).
      # Record timestamps are stored in UTC regardless.
      TZ: ${TZ:-UTC}
      # optional; when set it must be exactly 32 characters
      PB_ENCRYPTION_KEY: ${PB_ENCRYPTION_KEY:-}
    deploy:
      resources:
        limits:
          cpus: "${PB_CPU_LIMIT:-1.0}"
          memory: ${PB_MEM_LIMIT:-512m}
          pids: 256
    # hardening: the app needs no extra privileges and no capabilities.
    # It writes only to /pb_data, which is a volume.
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "${PB_PORT}:8090"
    volumes:
      # uploaded files and generated backups. Your records live in Postgres,
      # so this volume alone is NOT a complete backup.
      - pbdata:/pb_data

volumes:
  pgdata:
  pbdata:
COMPOSE_EOF

umask 077
cat > .env <<ENV_EOF
# Generated by install.sh. Keep this file private - it holds your database
# password.

POSTGRES_USER=$DB_USER
POSTGRES_DB=$DB_NAME
POSTGRES_PASSWORD=$DB_PASS

PB_IMAGE=$REPO_IMAGE:$IMAGE_TAG
PB_PORT=$PB_PORT

# Affects log timestamps and cron schedules such as automatic backups.
# Record timestamps are stored in UTC regardless of this.
TZ=$TZ_NAME

# Optional. Exactly 32 characters. Encrypts stored SMTP/S3 credentials at rest;
# losing it makes those settings unreadable. Generate one with:
#   openssl rand -base64 24 | cut -c1-32
PB_ENCRYPTION_KEY=

# Resource caps, not reservations. Too low and the kernel OOM-kills the
# process; raise them on a busy instance. 0 means unlimited.
# Change them later with:  sh install.sh --resources
PB_CPU_LIMIT=$PB_CPU
PB_MEM_LIMIT=$PB_MEM
PG_CPU_LIMIT=$PG_CPU
PG_MEM_LIMIT=$PG_MEM
ENV_EOF
umask 022

say ""
say "wrote $(pwd)/compose.yaml"
say "wrote $(pwd)/.env"
say ""

# ------------------------------------------------------------------ start ---
say "starting..."
$COMPOSE up -d

if ! wait_for_health "$PB_PORT"; then
    say "the server did not become healthy in time. Check the logs with:"
    say "  cd $(pwd) && $COMPOSE logs"
    exit 1
fi

# ------------------------------------------------------------ superuser -----
if [ -z "$ADMIN_EMAIL" ] && interactive; then
    ask ADMIN_EMAIL "First superuser email (blank to skip)" ""
fi

if [ -n "$ADMIN_EMAIL" ]; then
    valid_email "$ADMIN_EMAIL" || {
        if interactive; then
            ask_valid ADMIN_EMAIL "First superuser email" "$ADMIN_EMAIL" \
                      valid_email "that does not look like an email address"
        else
            die "invalid superuser email '$ADMIN_EMAIL'"
        fi
    }

    if [ -z "$ADMIN_PASS" ]; then
        ask_valid ADMIN_PASS "First superuser password (at least $MIN_PASSWORD_LEN characters)" "" \
                  valid_password "password must be at least $MIN_PASSWORD_LEN characters"
    else
        valid_password "$ADMIN_PASS" || die "superuser password must be at least $MIN_PASSWORD_LEN characters"
    fi

    if $COMPOSE exec -T pocketbase pb superuser upsert "$ADMIN_EMAIL" "$ADMIN_PASS"; then
        :
    else
        say "could not create the superuser. Create one later with:"
        say "  cd $(pwd) && $COMPOSE exec pocketbase pb superuser upsert EMAIL PASSWORD"
    fi
fi

say ""
say "----------------------------------------------------------------"
say " PocketBase is running"
say ""
say "   dashboard : http://localhost:$PB_PORT/_/"
say "   directory : $(pwd)"
say "   timezone  : $TZ_NAME"
say "   limits    : app ${PB_MEM}/${PB_CPU}cpu, database ${PG_MEM}/${PG_CPU}cpu"
say ""
say "   logs      : cd $(pwd) && $COMPOSE logs -f"
say "   stop      : cd $(pwd) && $COMPOSE down"
say "   update    : cd $(pwd) && sh install.sh --update"
say "   resources : cd $(pwd) && sh install.sh --resources"
say "   superuser : cd $(pwd) && $COMPOSE exec pocketbase pb superuser upsert EMAIL PASSWORD"
say ""
say " Your database password is in .env - back that file up."
say "----------------------------------------------------------------"
