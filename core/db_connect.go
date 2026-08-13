package core

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/fatih/color"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pocketbase/dbx"
)

const (
	// DefaultDBUrl is the connection string used when neither the --dbUrl flag
	// nor the PB_DB_URL env variable is set. It matches the dev service in the
	// bundled docker-compose.yml.
	DefaultDBUrl = "postgres://pocketbase:pocketbase@localhost:5432/pocketbase?sslmode=disable"

	// DataSchemaName is the Postgres schema holding the collection/record tables.
	DataSchemaName = "public"

	// AuxSchemaName is the Postgres schema holding the auxiliary (logs) tables.
	//
	// The auxiliary store lives in the same database as the main data but under
	// its own schema, since Postgres has no equivalent of attaching a second
	// SQLite file.
	AuxSchemaName = "pb_aux"
)

// warnIfConnectionBudgetExceeded reports when the configured pools could open
// more connections than the server will accept.
//
// The four pools (data read/write, aux read/write) are sized independently, so
// it is easy to raise one and quietly push the total past max_connections. The
// failure mode is not graceful - once the server is full, new connections are
// refused outright rather than queued - and it only shows up under load, so it
// is worth saying something at startup instead.
func (app *BaseApp) warnIfConnectionBudgetExceeded(db *sql.DB) {
	var rawMax, rawReserved string

	if err := db.QueryRow("SHOW max_connections").Scan(&rawMax); err != nil {
		return // not worth failing a boot over
	}
	maxConns, err := strconv.Atoi(strings.TrimSpace(rawMax))
	if err != nil || maxConns <= 0 {
		return
	}

	// best effort - older servers may not expose it
	reserved := 0
	if err := db.QueryRow("SHOW superuser_reserved_connections").Scan(&rawReserved); err == nil {
		reserved, _ = strconv.Atoi(strings.TrimSpace(rawReserved))
	}

	budget := app.config.DataMaxOpenConns + app.config.DataWriteMaxOpenConns +
		app.config.AuxMaxOpenConns + app.config.AuxWriteMaxOpenConns

	usable := maxConns - reserved
	if budget <= usable {
		return
	}

	color.Yellow(
		"[pocketbase] connection pools may open %d connections but the server allows %d"+
			" (max_connections=%d, %d reserved).",
		budget, usable, maxConns, reserved,
	)
	color.HiBlack(
		"  data read=%d write=%d, aux read=%d write=%d."+
			" Lower them, raise max_connections, or use a connection pooler.",
		app.config.DataMaxOpenConns, app.config.DataWriteMaxOpenConns,
		app.config.AuxMaxOpenConns, app.config.AuxWriteMaxOpenConns,
	)
}

// DefaultDBConnect opens a new Postgres connection pool for the provided DSN.
func DefaultDBConnect(dsn string) (*dbx.DB, error) {
	db, err := dbx.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	return db, nil
}

// NewSimpleProtocolDB opens a new connection that executes statements with the
// pgx simple protocol.
//
// This is needed for raw multi-statement SQL (eg. the superuser SQL console),
// because the default extended protocol rejects more than one command per
// query. The caller is responsible for closing the returned db.
func NewSimpleProtocolDB(dbUrl string, schema string) (*dbx.DB, error) {
	dsn, err := dsnWithSearchPath(dbUrl, schema)
	if err != nil {
		return nil, err
	}

	dsn, err = dsnWithParam(dsn, "default_query_exec_mode", "simple_protocol")
	if err != nil {
		return nil, err
	}

	db, err := DefaultDBConnect(dsn)
	if err != nil {
		return nil, err
	}

	db.DB().SetMaxOpenConns(1)
	db.DB().SetMaxIdleConns(1)

	return db, nil
}

// dsnWithParam returns a copy of the provided Postgres DSN with the specified
// runtime parameter set.
func dsnWithParam(dsn string, key string, value string) (string, error) {
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn + " " + key + "=" + value, nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("failed to parse db url: %w", err)
	}

	q := parsed.Query()
	q.Set(key, value)
	parsed.RawQuery = q.Encode()

	return parsed.String(), nil
}

// dsnWithSearchPath returns a copy of the provided Postgres DSN with its
// search_path runtime parameter set to the specified schema.
func dsnWithSearchPath(dsn string, schema string) (string, error) {
	// keyword/value DSNs ("host=... user=...") are not URL-shaped, so the
	// parameter is appended instead of being set as a query value
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn + " search_path=" + schema, nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("failed to parse db url: %w", err)
	}

	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()

	return parsed.String(), nil
}
