package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// PgDumpFileName is the name of the database dump stored inside a backup archive.
const PgDumpFileName = "pb_database.sql"

// pgToolTimeout caps how long a pg_dump/psql invocation may run.
const pgToolTimeout = 30 * time.Minute

// pgDump writes a plain-SQL dump of the app schemas into destPath.
//
// It shells out to pg_dump, which must be available in PATH.
func pgDump(ctx context.Context, app App, destPath string) error {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return errors.New("pg_dump was not found in PATH - it is required to create database backups")
	}

	ctx, cancel := context.WithTimeout(ctx, pgToolTimeout)
	defer cancel()

	//nolint:gosec // the arguments are app configuration, not user input
	cmd := exec.CommandContext(ctx, "pg_dump",
		"--dbname="+app.DBUrl(),
		"--schema="+app.DataSchemaName(),
		"--schema="+app.AuxSchemaName(),
		"--format=plain",
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--quote-all-identifiers",
		"--file="+destPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump failed: %w (%s)", err, stderr.String())
	}

	return nil
}

// pgRestore loads a plain-SQL dump produced by [pgDump] back into the database.
//
// It shells out to psql, which must be available in PATH.
func pgRestore(ctx context.Context, app App, dumpPath string) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return errors.New("psql was not found in PATH - it is required to restore database backups")
	}

	ctx, cancel := context.WithTimeout(ctx, pgToolTimeout)
	defer cancel()

	//nolint:gosec // the arguments are app configuration, not user input
	cmd := exec.CommandContext(ctx, "psql",
		"--dbname="+app.DBUrl(),
		"--set=ON_ERROR_STOP=1",
		"--single-transaction",
		"--file="+dumpPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql restore failed: %w (%s)", err, stderr.String())
	}

	return nil
}
