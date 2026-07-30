package migrations

import (
	"github.com/pocketbase/pocketbase/core"
)

// Adds the "_rowid_" sequence column to the logs table.
//
// Record tables get it from core.SyncRecordTableSchema, but the aux "_logs"
// table is created by a plain DDL statement and was missing it, which broke the
// logs UI because it sorts by "-@rowid" by default.
//
// New installations already get the column from the aux init migration, so this
// is only meaningful for databases created before it was added.
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			_, err := txApp.AuxDB().NewQuery(
				`ALTER TABLE {{_logs}} ADD COLUMN IF NOT EXISTS [[_rowid_]] BIGSERIAL NOT NULL`,
			).Execute()

			return err
		},
		Down: func(txApp core.App) error {
			_, err := txApp.AuxDB().NewQuery(
				`ALTER TABLE {{_logs}} DROP COLUMN IF EXISTS [[_rowid_]]`,
			).Execute()

			return err
		},
	})
}
