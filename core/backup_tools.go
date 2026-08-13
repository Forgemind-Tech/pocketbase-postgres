package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/tools/types"
)

// paramsKeyBackupTools is the _params row holding the backup tool overrides.
//
// It is deliberately a separate row rather than a field of the app Settings:
// Settings are writable through PATCH /api/settings, and these values are
// executed as a host command, so putting them there would let any superuser run
// arbitrary commands on the server. Keeping them out of the settings payload
// means changing them requires host access, the same bar as before.
const paramsKeyBackupTools = "backupTools"

// BackupToolsConfig holds the commands used by the backup feature.
//
// Empty fields mean "look the tool up in PATH".
type BackupToolsConfig struct {
	// PgDump is the command used to create backups, eg.
	// "docker exec -i pocketbase-postgres pg_dump".
	PgDump string `json:"pgDump"`

	// Psql is the command used to restore backups.
	Psql string `json:"psql"`
}

// BackupToolsConfig returns the stored backup tool overrides.
//
// A missing row, or any failure to read it, yields the zero value so that the
// caller falls back to PATH - the resulting "not available" error is far more
// useful than one about the config lookup itself.
func (app *BaseApp) BackupToolsConfig() BackupToolsConfig {
	var config BackupToolsConfig

	param := &Param{}
	if err := app.ModelQuery(param).Model(paramsKeyBackupTools, param); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			app.Logger().Warn("Failed to load the backup tools config", "error", err)
		}

		return config
	}

	if err := json.Unmarshal(param.Value, &config); err != nil {
		app.Logger().Warn("Failed to parse the backup tools config", "error", err)
	}

	return config
}

// SaveBackupToolsConfig persists the backup tool overrides.
func (app *BaseApp) SaveBackupToolsConfig(config BackupToolsConfig) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}

	param := &Param{}
	err = app.ModelQuery(param).Model(paramsKeyBackupTools, param)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	param.Value = types.JSONRaw(raw)

	if param.Id == "" {
		param.Id = paramsKeyBackupTools
		param.Created = types.NowDateTime()
		param.MarkAsNew()
	}
	param.Updated = types.NowDateTime()

	return app.Save(param)
}

// BundledPostgresContainer is the container_name pinned by the bundled
// docker-compose.yml.
const BundledPostgresContainer = "pocketbase-postgres"

// bundledContainerLookupTimeout caps the "is the bundled container running"
// probe, so a wedged Docker daemon cannot stall a backup.
const bundledContainerLookupTimeout = 5 * time.Second

// resolveBackupTool returns the argv for a backup tool, in descending priority:
//
//  1. the env variable
//  2. the value stored in the database
//  3. the tool in PATH
//  4. the tool inside the bundled Postgres container, if it is running
//
// Step 4 exists because the tools are genuinely absent on a typical Go-only
// development host, and both configurable locations are wiped by routine
// resets - deleting pb_data, or recreating the database volume. Falling back to
// the container our own compose file defines makes the bundled setup work with
// no configuration at all, instead of breaking every time something is reset.
func resolveBackupTool(app App, envKey string, stored string, fallback string) []string {
	if v := strings.Fields(os.Getenv(envKey)); len(v) > 0 {
		return v
	}

	if v := strings.Fields(stored); len(v) > 0 {
		return v
	}

	if _, err := exec.LookPath(fallback); err == nil {
		return []string{fallback}
	}

	if argv := bundledContainerCommand(fallback); argv != nil {
		return argv
	}

	// nothing worked - return the plain name so the caller reports the
	// familiar "not available" error rather than something about Docker
	return []string{fallback}
}

// bundledContainerCommand returns a command running bin inside the bundled
// Postgres container, or nil if that container is not available.
func bundledContainerCommand(bin string) []string {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), bundledContainerLookupTimeout)
	defer cancel()

	out, err := exec.CommandContext(
		ctx, "docker", "inspect", "-f", "{{.State.Running}}", BundledPostgresContainer,
	).Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil
	}

	return []string{"docker", "exec", "-i", BundledPostgresContainer, bin}
}
