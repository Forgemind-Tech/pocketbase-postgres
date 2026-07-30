package migrations

import (
	"github.com/pocketbase/pocketbase/core"
)

func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			// note: the statements are executed one by one because the pgx
			// extended protocol rejects multiple commands in a single query
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS {{_logs}} (
					[[id]]      TEXT PRIMARY KEY DEFAULT ('r'||substr(md5(random()::text), 1, 14)) NOT NULL,
					[[level]]   INTEGER DEFAULT 0 NOT NULL,
					[[message]] TEXT DEFAULT '' NOT NULL,
					[[data]]    JSONB DEFAULT '{}' NOT NULL,
					[[created]] TEXT DEFAULT (to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.MS') || 'Z') NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_logs_level on {{_logs}} ([[level]])`,
				`CREATE INDEX IF NOT EXISTS idx_logs_message on {{_logs}} ([[message]])`,
				// note: the created value is a fixed-width "YYYY-MM-DD HH:MM:SS.mmmZ"
				// string, so it is sliced to hour precision rather than parsed as a
				// timestamp - casting to timestamptz is only STABLE and Postgres
				// rejects non-IMMUTABLE functions in index expressions
				`CREATE INDEX IF NOT EXISTS idx_logs_created_hour on {{_logs}} (substr([[created]], 1, 13))`,
			}

			for _, stmt := range stmts {
				if _, err := txApp.AuxDB().NewQuery(stmt).Execute(); err != nil {
					return err
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.AuxDB().DropTable("_logs").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			// reapply only if the _logs table doesn't exist
			exists := txApp.AuxHasTable("_logs")
			return !exists, nil
		},
	})
}
