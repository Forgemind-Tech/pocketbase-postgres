package core

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/pocketbase/pocketbase/tools/security"
)

// testDBUrl returns the Postgres connection string used by the core tests.
func testDBUrl() string {
	if env := os.Getenv("PB_TEST_DB_URL"); env != "" {
		return env
	}

	return "postgres://pocketbase:pocketbase@localhost:5433/pocketbase_test?sslmode=disable"
}

var coreTestSchemaCounter int64

// isolatedTestConfig fills the provided config with a connection string and a
// unique pair of schemas.
//
// Tests in this package construct a BaseApp directly instead of going through
// the tests package (which would be an import cycle), so without this they
// would all share the default database and interfere with each other.
func isolatedTestConfig(t *testing.T, config BaseAppConfig) BaseAppConfig {
	t.Helper()

	suffix := fmt.Sprintf(
		"%s_%d",
		security.PseudorandomStringWithAlphabet(8, "abcdefghijklmnopqrstuvwxyz0123456789"),
		atomic.AddInt64(&coreTestSchemaCounter, 1),
	)

	if config.DBUrl == "" {
		config.DBUrl = testDBUrl()
	}
	config.DataSchema = "pb_core_" + suffix
	config.AuxSchema = "pb_core_aux_" + suffix

	// keep the pools small - many of these apps run in parallel
	config.DataMaxOpenConns = 4
	config.DataMaxIdleConns = 2
	config.AuxMaxOpenConns = 2
	config.AuxMaxIdleConns = 1

	db, err := DefaultDBConnect(config.DBUrl)
	if err != nil {
		t.Fatalf("Failed to connect for schema setup: %v", err)
	}
	defer db.Close()

	for _, schema := range []string{config.DataSchema, config.AuxSchema} {
		if _, err := db.NewQuery(fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)).Execute(); err != nil {
			t.Fatalf("Failed to reset schema %s: %v", schema, err)
		}
		if _, err := db.NewQuery(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Execute(); err != nil {
			t.Fatalf("Failed to create schema %s: %v", schema, err)
		}
	}

	dataSchema, auxSchema := config.DataSchema, config.AuxSchema
	t.Cleanup(func() {
		cleanupDB, err := DefaultDBConnect(testDBUrl())
		if err != nil {
			return
		}
		defer cleanupDB.Close()

		for _, schema := range []string{dataSchema, auxSchema} {
			_, _ = cleanupDB.NewQuery(fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)).Execute()
		}
	})

	return config
}
