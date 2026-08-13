package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// PgDumpFileName is the name of the database dump stored inside a backup archive.
const PgDumpFileName = "pb_database.sql"

// pgToolTimeout caps how long a pg_dump/psql invocation may run.
const pgToolTimeout = 30 * time.Minute

// PgDumpCommand and PgRestoreCommand resolve the commands used to dump and
// restore the database, in descending priority:
//
//  1. the PB_PG_DUMP / PB_PSQL env variables
//  2. the values stored in the database (see BackupToolsConfig)
//  3. a plain "pg_dump" / "psql" looked up in PATH
//
// They live in the database rather than in a file under pb_data because backups
// are usually triggered from the admin UI of a running server, and because
// pb_data is now disposable - records live in Postgres, so wiping it is a
// normal reset that must not silently break backups.
//
// The usual reason to override them is to run the tools inside the Postgres
// container, so a host with nothing but Go installed can still take backups:
//
//	pocketbase backup-tools --pgDump "docker exec -i pocketbase-postgres pg_dump"
//
// The dump is streamed over stdout/stdin rather than written with --file so that
// the file always lands on the host, even when the tool runs in a container.
//
// The value is split on spaces, so paths containing spaces are not supported;
// use a wrapper script in that case.
func PgDumpCommand(app App) []string {
	return resolveBackupTool(app, "PB_PG_DUMP", app.BackupToolsConfig().PgDump, "pg_dump")
}

// PgRestoreCommand resolves the restore command, see [PgDumpCommand].
func PgRestoreCommand(app App) []string {
	return resolveBackupTool(app, "PB_PSQL", app.BackupToolsConfig().Psql, "psql")
}

// pgToolError builds the "not available" error for a missing tool.
func pgToolError(bin string, envKey string, setFlag string, err error) error {
	return fmt.Errorf(
		"%q is not available (%w)\n"+
			"  install the Postgres client tools, or point PocketBase at a copy it can reach.\n"+
			"  to run it inside the bundled Postgres container:\n"+
			"    pocketbase backup-tools --%s \"docker exec -i pocketbase-postgres %s\"\n"+
			"  or set the %s env variable to the same value before starting the server",
		bin, err, setFlag, bin, envKey,
	)
}

// pgDump writes a plain-SQL dump of the app schemas into destPath.
func pgDump(ctx context.Context, app App, destPath string) error {
	argv := PgDumpCommand(app)

	if _, err := exec.LookPath(argv[0]); err != nil {
		return pgToolError(argv[0], "PB_PG_DUMP", "pgDump", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pgToolTimeout)
	defer cancel()

	args := append(argv[1:],
		"--dbname="+app.DBUrl(),
		"--schema="+app.DataSchemaName(),
		"--schema="+app.AuxSchemaName(),
		"--format=plain",
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--quote-all-identifiers",
	)

	dest, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dest.Close()

	//nolint:gosec // the arguments are app configuration, not user input
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Stdout = dest

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump failed: %w (%s)", err, stderr.String())
	}

	// a dump that failed to write anything would otherwise produce a backup
	// archive that silently restores nothing
	info, err := dest.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("pg_dump produced an empty dump (%s)", stderr.String())
	}

	return nil
}

// pgRestore loads a plain-SQL dump produced by [pgDump] back into the database.
func pgRestore(ctx context.Context, app App, dumpPath string) error {
	argv := PgRestoreCommand(app)

	if _, err := exec.LookPath(argv[0]); err != nil {
		return pgToolError(argv[0], "PB_PSQL", "psql", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pgToolTimeout)
	defer cancel()

	args := append(argv[1:],
		"--dbname="+app.DBUrl(),
		"--set=ON_ERROR_STOP=1",
		"--single-transaction",
	)

	dump, err := os.Open(dumpPath)
	if err != nil {
		return err
	}
	defer dump.Close()

	//nolint:gosec // the arguments are app configuration, not user input
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Stdin = dump

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// psql reads the script from stdin, so anything it prints on stdout is
	// progress noise
	cmd.Stdout = io.Discard

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql restore failed: %w (%s)", err, stderr.String())
	}

	return nil
}
