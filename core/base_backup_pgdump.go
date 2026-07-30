package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PgDumpFileName is the name of the database dump stored inside a backup archive.
const PgDumpFileName = "pb_database.sql"

// pgToolTimeout caps how long a pg_dump/psql invocation may run.
const pgToolTimeout = 30 * time.Minute

// pgDumpCommand and pgRestoreCommand are the commands used to dump and restore
// the database.
//
// They default to a plain "pg_dump"/"psql" in PATH, but can be overridden with
// the PB_PG_DUMP and PB_PSQL env variables to run the tools somewhere else -
// most commonly inside the Postgres container, so that a host with nothing but
// Go installed can still create backups, eg.:
//
//	PB_PG_DUMP="docker compose exec -T postgres pg_dump"
//	PB_PSQL="docker compose exec -T postgres psql"
//
// The dump is streamed over stdout/stdin rather than written with --file so that
// the file always lands on the host, even when the tool runs in a container.
//
// The value is split on spaces, so paths containing spaces are not supported;
// use a wrapper script in that case.

// PgDumpCommand exposes the resolved dump command, so that callers (eg. the
// tests) can tell whether the tool is reachable before relying on backups.
func PgDumpCommand() []string {
	return pgDumpCommand()
}

// PgRestoreCommand exposes the resolved restore command.
func PgRestoreCommand() []string {
	return pgRestoreCommand()
}

func pgDumpCommand() []string {
	return pgToolCommand("PB_PG_DUMP", "pg_dump")
}

func pgRestoreCommand() []string {
	return pgToolCommand("PB_PSQL", "psql")
}

func pgToolCommand(envKey string, fallback string) []string {
	if v := strings.Fields(os.Getenv(envKey)); len(v) > 0 {
		return v
	}

	return []string{fallback}
}

// pgToolError builds the "not available" error for a missing tool.
func pgToolError(bin string, envKey string, err error) error {
	return fmt.Errorf(
		"%q is not available (%w) - install the Postgres client tools, or set %s to run it elsewhere"+
			` (eg. %s="docker compose exec -T postgres %s")`,
		bin, err, envKey, envKey, bin,
	)
}

// pgDump writes a plain-SQL dump of the app schemas into destPath.
func pgDump(ctx context.Context, app App, destPath string) error {
	argv := pgDumpCommand()

	if _, err := exec.LookPath(argv[0]); err != nil {
		return pgToolError(argv[0], "PB_PG_DUMP", err)
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
	argv := pgRestoreCommand()

	if _, err := exec.LookPath(argv[0]); err != nil {
		return pgToolError(argv[0], "PB_PSQL", err)
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
