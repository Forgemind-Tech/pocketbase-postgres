package core

import (
	"fmt"
	"net/url"
	"strings"

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
