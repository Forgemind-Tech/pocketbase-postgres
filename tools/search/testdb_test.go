package search_test

import (
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// testDBUrl returns the Postgres connection string used by these tests.
//
// It mirrors the helper in the internal search package tests, since the two
// live in different packages.
func testDBUrl() string {
	if env := os.Getenv("PB_TEST_DB_URL"); env != "" {
		return env
	}

	return "postgres://pocketbase:pocketbase@localhost:5433/pocketbase_test?sslmode=disable"
}
