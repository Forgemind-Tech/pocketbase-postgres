package core

import (
	"database/sql"
	"fmt"

	"github.com/pocketbase/dbx"
)

// TableColumns returns all column names of a single table by its name.
func (app *BaseApp) TableColumns(tableName string) ([]string, error) {
	columns := []string{}

	err := app.ConcurrentDB().NewQuery(`
		SELECT a.attname
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()
			AND c.relname = {:tableName}
			AND a.attnum > 0
			AND NOT a.attisdropped
			AND a.attname != '_rowid_'
		ORDER BY a.attnum
	`).
		Bind(dbx.Params{"tableName": tableName}).
		Column(&columns)

	return columns, err
}

type TableInfoRow struct {
	// the `db:"pk"` tag has special semantic so we cannot rename
	// the original field without specifying a custom mapper
	PK int

	Index        int            `db:"cid"`
	Name         string         `db:"name"`
	Type         string         `db:"type"`
	NotNull      bool           `db:"notnull"`
	DefaultValue sql.NullString `db:"dflt_value"`
}

// TableInfo returns the column definitions of the specified table.
func (app *BaseApp) TableInfo(tableName string) ([]*TableInfoRow, error) {
	info := []*TableInfoRow{}

	err := app.ConcurrentDB().NewQuery(`
		SELECT
			(a.attnum - 1) AS cid,
			a.attname AS name,
			format_type(a.atttypid, a.atttypmod) AS type,
			a.attnotnull AS notnull,
			pg_get_expr(d.adbin, d.adrelid) AS dflt_value,
			CASE WHEN ct.conkey @> ARRAY[a.attnum] THEN 1 ELSE 0 END AS pk
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		LEFT JOIN pg_constraint ct ON ct.conrelid = c.oid AND ct.contype = 'p'
		WHERE n.nspname = current_schema()
			AND c.relname = {:tableName}
			AND a.attnum > 0
			AND NOT a.attisdropped
			AND a.attname != '_rowid_'
		ORDER BY a.attnum
	`).
		Bind(dbx.Params{"tableName": tableName}).
		All(&info)
	if err != nil {
		return nil, err
	}

	// a missing table yields no rows rather than an error
	if len(info) == 0 {
		return nil, fmt.Errorf("empty table info probably due to invalid or missing table %s", tableName)
	}

	return info, nil
}

// TableIndexes returns a name grouped map with all non empty index of the specified table.
//
// The returned values are the full "CREATE INDEX" definitions as reported by Postgres.
//
// Note: This method doesn't return an error on nonexisting table.
func (app *BaseApp) TableIndexes(tableName string) (map[string]string, error) {
	indexes := []struct {
		Name string
		Sql  string
	}{}

	// note: indexes backing a constraint (eg. the implicit primary key index)
	// are excluded because they are not user/collection managed - SQLite did
	// not report them either
	err := app.ConcurrentDB().NewQuery(`
		SELECT i.indexname AS name, i.indexdef AS sql
		FROM pg_indexes i
		JOIN pg_class c ON c.relname = i.indexname
		JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = i.schemaname
		WHERE i.schemaname = current_schema()
			AND i.tablename = {:tableName}
			AND i.indexdef IS NOT NULL
			AND NOT EXISTS (
				SELECT 1 FROM pg_constraint con WHERE con.conindid = c.oid
			)
	`).
		Bind(dbx.Params{"tableName": tableName}).
		All(&indexes)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(indexes))

	for _, idx := range indexes {
		result[idx.Name] = idx.Sql
	}

	return result, nil
}

// DeleteTable drops the specified table.
//
// This method is a no-op if a table with the provided name doesn't exist.
//
// NB! Be aware that this method is vulnerable to SQL injection and the
// "dangerousTableName" argument must come only from trusted input!
func (app *BaseApp) DeleteTable(dangerousTableName string) error {
	_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(
		"DROP TABLE IF EXISTS {{%s}} CASCADE",
		dangerousTableName,
	)).Execute()

	return err
}

// HasTable checks if a table (or view) with the provided name exists (case insensitive)
// in the data schema.
func (app *BaseApp) HasTable(tableName string) bool {
	return app.hasTable(app.ConcurrentDB(), tableName)
}

// AuxHasTable checks if a table (or view) with the provided name exists (case insensitive)
// in the auxiliary schema.
func (app *BaseApp) AuxHasTable(tableName string) bool {
	return app.hasTable(app.AuxConcurrentDB(), tableName)
}

func (app *BaseApp) hasTable(db dbx.Builder, tableName string) bool {
	var exists int

	err := db.NewQuery(`
		SELECT 1
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()
			AND c.relkind IN ('r', 'v', 'm', 'p')
			AND LOWER(c.relname) = LOWER({:tableName})
		LIMIT 1
	`).
		Bind(dbx.Params{"tableName": tableName}).
		Row(&exists)

	return err == nil && exists > 0
}

// Vacuum executes VACUUM on the data schema tables in order to reclaim unused disk space.
func (app *BaseApp) Vacuum() error {
	return app.vacuum(app.NonconcurrentDB())
}

// AuxVacuum executes VACUUM on the auxiliary schema tables in order to reclaim unused disk space.
func (app *BaseApp) AuxVacuum() error {
	return app.vacuum(app.AuxNonconcurrentDB())
}

func (app *BaseApp) vacuum(db dbx.Builder) error {
	// note: VACUUM cannot run inside a transaction block, so this must never
	// be called with a transactional builder
	_, err := db.NewQuery("VACUUM").Execute()

	return err
}
