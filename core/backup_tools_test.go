package core_test

import (
	"os"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestBackupToolsConfigRoundtrip(t *testing.T) {
	t.Parallel()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	// nothing stored yet
	if config := app.BackupToolsConfig(); config.PgDump != "" || config.Psql != "" {
		t.Fatalf("expected an empty config, got %#v", config)
	}

	if err := app.SaveBackupToolsConfig(core.BackupToolsConfig{
		PgDump: "docker exec -i c pg_dump",
		Psql:   "docker exec -i c psql",
	}); err != nil {
		t.Fatal(err)
	}

	loaded := app.BackupToolsConfig()
	if loaded.PgDump != "docker exec -i c pg_dump" || loaded.Psql != "docker exec -i c psql" {
		t.Fatalf("unexpected roundtrip result: %#v", loaded)
	}

	// update rather than insert the second time
	if err := app.SaveBackupToolsConfig(core.BackupToolsConfig{PgDump: "changed", Psql: "changed2"}); err != nil {
		t.Fatal(err)
	}
	if loaded := app.BackupToolsConfig(); loaded.PgDump != "changed" {
		t.Fatalf("expected the update to apply, got %#v", loaded)
	}
}

func TestBackupToolsResolution(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	// note: not parallel - it mutates the environment

	t.Run("falls back to PATH or the bundled container", func(t *testing.T) {
		// which one applies depends on the machine: a host with the client
		// tools installed resolves to PATH, a Go-only host with the bundled
		// compose running resolves to the container
		argv := core.PgDumpCommand(app)
		if len(argv) == 0 {
			t.Fatal("expected a non-empty command")
		}
		if argv[0] != "pg_dump" && argv[0] != "docker" {
			t.Fatalf("expected pg_dump or docker, got %v", argv)
		}
	})

	t.Run("the database value wins over the fallback", func(t *testing.T) {
		if err := app.SaveBackupToolsConfig(core.BackupToolsConfig{PgDump: "docker exec -i c pg_dump"}); err != nil {
			t.Fatal(err)
		}

		argv := core.PgDumpCommand(app)
		if len(argv) != 5 || argv[0] != "docker" || argv[4] != "pg_dump" {
			t.Fatalf("expected the stored command, got %v", argv)
		}
	})

	t.Run("the env variable wins over the database", func(t *testing.T) {
		os.Setenv("PB_PG_DUMP", "/usr/local/bin/pg_dump")
		defer os.Unsetenv("PB_PG_DUMP")

		argv := core.PgDumpCommand(app)
		if len(argv) != 1 || argv[0] != "/usr/local/bin/pg_dump" {
			t.Fatalf("expected the env value to win, got %v", argv)
		}
	})
}
