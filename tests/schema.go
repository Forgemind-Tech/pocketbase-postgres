package tests

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

// DefaultTestDBUrl points at the "postgres-test" service of the bundled
// docker-compose.yml.
const DefaultTestDBUrl = "postgres://pocketbase:pocketbase@localhost:5433/pocketbase_test?sslmode=disable"

// templateDBName is the database that holds a fully migrated and seeded state.
//
// Every test database is created as a copy of it, which is dramatically cheaper
// than re-running the migrations and the fixture per test app (and generates far
// less WAL, which matters for a suite with hundreds of scenarios).
//
// The name is process specific because "go test ./..." runs the package test
// binaries in parallel and they would otherwise drop each other's template.
var templateDBName = fmt.Sprintf("pb_test_template_%d", os.Getpid())

// TestDBUrl returns the Postgres connection string used by the test suite.
//
// It can be overridden with the PB_TEST_DB_URL env variable.
func TestDBUrl() string {
	if env := os.Getenv("PB_TEST_DB_URL"); env != "" {
		return env
	}

	return DefaultTestDBUrl
}

var testDBCounter int64

// testDBName returns a process-unique test database name.
//
// The random part keeps concurrently running "go test" processes (eg. one per
// package) from colliding, while the counter keeps it unique within a process.
// The pid is included so that any leftover from a crashed run can be traced
// back to the process that created it.
func testDBName() string {
	n := atomic.AddInt64(&testDBCounter, 1)

	return fmt.Sprintf(
		"pb_test_%d_%s_%d",
		os.Getpid(),
		security.PseudorandomStringWithAlphabet(6, "abcdefghijklmnopqrstuvwxyz0123456789"),
		n,
	)
}

// dbUrlWithDatabase returns a copy of the provided connection string pointing
// at another database on the same server.
func dbUrlWithDatabase(dbUrl string, name string) (string, error) {
	parsed, err := url.Parse(dbUrl)
	if err != nil {
		return "", fmt.Errorf("failed to parse the test db url: %w", err)
	}

	parsed.Path = "/" + name

	return parsed.String(), nil
}

var (
	templateOnce sync.Once
	templateErr  error
)

// ensureTemplateDB builds the shared template database once per process.
func ensureTemplateDB() error {
	templateOnce.Do(func() {
		templateErr = buildTemplateDB()
	})

	return templateErr
}

func buildTemplateDB() error {
	adminDB, err := core.DefaultDBConnect(TestDBUrl())
	if err != nil {
		return fmt.Errorf("failed to connect to the test server: %w", err)
	}
	defer adminDB.Close()

	// note: FORCE terminates any leftover connections from a previous run
	_, err = adminDB.NewQuery(fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, templateDBName)).Execute()
	if err != nil {
		return fmt.Errorf("failed to drop the template db: %w", err)
	}

	if _, err := adminDB.NewQuery(fmt.Sprintf(`CREATE DATABASE %q`, templateDBName)).Execute(); err != nil {
		return fmt.Errorf("failed to create the template db: %w", err)
	}

	templateUrl, err := dbUrlWithDatabase(TestDBUrl(), templateDBName)
	if err != nil {
		return err
	}

	// migrate and seed the template through a regular app so that the state is
	// produced by the same code path as a real installation
	tempDir, err := os.MkdirTemp("", "pb_test_template")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:          filepath.Join(tempDir, "pb_data"),
		DBUrl:            templateUrl,
		EncryptionEnv:    "pb_test_env",
		DataMaxOpenConns: 2,
		DataMaxIdleConns: 1,
		AuxMaxOpenConns:  1,
		AuxMaxIdleConns:  1,
	})

	if err := app.Bootstrap(); err != nil {
		return fmt.Errorf("failed to bootstrap the template app: %w", err)
	}

	if err := app.RunAllMigrations(); err != nil {
		app.ResetBootstrapState()
		return fmt.Errorf("failed to migrate the template db: %w", err)
	}

	if err := seedFixture(app); err != nil {
		app.ResetBootstrapState()
		return fmt.Errorf("failed to seed the template db: %w", err)
	}

	// release the connections so that the database can be used as a template
	if err := app.ResetBootstrapState(); err != nil {
		return fmt.Errorf("failed to close the template app: %w", err)
	}

	return nil
}

// createTestDatabase clones the template into a fresh database and returns its
// connection string.
func createTestDatabase(name string) (string, error) {
	if err := ensureTemplateDB(); err != nil {
		return "", err
	}

	adminDB, err := core.DefaultDBConnect(TestDBUrl())
	if err != nil {
		return "", err
	}
	defer adminDB.Close()

	err = retryOnTemplateInUse(func() error {
		_, execErr := adminDB.NewQuery(fmt.Sprintf(
			`CREATE DATABASE %q TEMPLATE %q`, name, templateDBName,
		)).Execute()
		return execErr
	})
	if err != nil {
		return "", fmt.Errorf("failed to create the test db %s: %w", name, err)
	}

	return dbUrlWithDatabase(TestDBUrl(), name)
}

// retryOnTemplateInUse retries while Postgres reports the template as busy,
// which happens when several tests clone it at the same time.
//
// The wait is bounded and backs off between attempts - retrying in a tight loop
// only adds load to the very server the tests are waiting on.
func retryOnTemplateInUse(op func() error) error {
	var err error

	deadline := time.Now().Add(2 * time.Minute)
	delay := 5 * time.Millisecond

	for {
		err = op()
		if err == nil || !strings.Contains(err.Error(), "being accessed by other users") {
			return err
		}

		if time.Now().After(deadline) {
			return err
		}

		time.Sleep(delay)
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

// dropTestDatabase removes a database created by createTestDatabase.
func dropTestDatabase(dbUrl string) error {
	name, err := databaseFromUrl(dbUrl)
	if err != nil || name == "" || name == templateDBName {
		return err
	}

	adminDB, err := core.DefaultDBConnect(TestDBUrl())
	if err != nil {
		return err
	}
	defer adminDB.Close()

	_, err = adminDB.NewQuery(fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)).Execute()

	return err
}

func databaseFromUrl(dbUrl string) (string, error) {
	parsed, err := url.Parse(dbUrl)
	if err != nil {
		return "", err
	}

	return strings.TrimPrefix(parsed.Path, "/"), nil
}
