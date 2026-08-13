package cmd

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// NewBackupToolsCommand creates and returns a new command for inspecting and
// changing the pg_dump/psql commands used by the backup feature.
//
// The values are stored in the database rather than exposed through the
// settings API, because they are executed as host commands - see
// core.paramsKeyBackupTools.
func NewBackupToolsCommand(app core.App) *cobra.Command {
	var pgDump, psql string

	command := &cobra.Command{
		Use:   "backup-tools",
		Short: "Shows or changes the pg_dump/psql commands used for backups",
		Long: "Shows or changes the pg_dump/psql commands used for backups.\n\n" +
			"Without flags it prints what currently resolves. The values are stored in\n" +
			"the database, so they survive pb_data being wiped and apply to backups\n" +
			"triggered from the admin UI.\n\n" +
			"They are intentionally not part of the settings API: the values are executed\n" +
			"as host commands, so changing them requires access to this CLI.",
		Example: `backup-tools --pgDump "docker exec -i pocketbase-postgres pg_dump" ` +
			`--psql "docker exec -i pocketbase-postgres psql"`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, args []string) error {
			flags := command.Flags()

			if flags.Changed("pgDump") || flags.Changed("psql") {
				config := app.BackupToolsConfig()

				// only touch what was actually passed, so setting one does not
				// silently clear the other
				if flags.Changed("pgDump") {
					config.PgDump = pgDump
				}
				if flags.Changed("psql") {
					config.Psql = psql
				}

				if err := app.SaveBackupToolsConfig(config); err != nil {
					return err
				}

				color.Green("Saved.")
				fmt.Println()
			}

			fmt.Println("resolved backup tools:")
			fmt.Printf("  pg_dump:  %s\n", color.CyanString(strings.Join(core.PgDumpCommand(app), " ")))
			fmt.Printf("  psql:     %s\n", color.CyanString(strings.Join(core.PgRestoreCommand(app), " ")))
			fmt.Println()
			color.HiBlack("resolution order: PB_PG_DUMP/PB_PSQL env, then the database, then PATH,")
			color.HiBlack("then the bundled Postgres container (%s) if it is running", core.BundledPostgresContainer)

			return nil
		},
	}

	command.Flags().StringVar(&pgDump, "pgDump", "",
		`command used to create backups, eg. "docker exec -i pocketbase-postgres pg_dump"`)
	command.Flags().StringVar(&psql, "psql", "",
		`command used to restore backups, eg. "docker exec -i pocketbase-postgres psql"`)

	return command
}
