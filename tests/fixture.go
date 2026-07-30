package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// fixtureData mirrors the structure of tests/data/fixture.json, which holds
// the seed data every test application starts from.
type fixtureData struct {
	Collections []map[string]any            `json:"collections"`
	Params      []map[string]any            `json:"params"`
	Records     map[string][]map[string]any `json:"records"`
	Logs        []map[string]any            `json:"logs"`
}

var (
	loadedFixture     *fixtureData
	loadedFixtureErr  error
	loadedFixtureOnce sync.Once
)

// loadFixture reads and caches the shared test fixture.
func loadFixture() (*fixtureData, error) {
	loadedFixtureOnce.Do(func() {
		_, currentFile, _, _ := runtime.Caller(0)
		fixturePath := filepath.Join(path.Dir(currentFile), "data", "fixture.json")

		raw, err := os.ReadFile(fixturePath)
		if err != nil {
			loadedFixtureErr = fmt.Errorf("failed to read the test fixture: %w", err)
			return
		}

		parsed := &fixtureData{}
		if err := json.Unmarshal(raw, parsed); err != nil {
			loadedFixtureErr = fmt.Errorf("failed to parse the test fixture: %w", err)
			return
		}

		loadedFixture = parsed
	})

	return loadedFixture, loadedFixtureErr
}

// seedFixture replaces the freshly migrated schema contents with the test fixture.
//
// The record tables are (re)built through the regular collection sync so that
// the tests exercise the same DDL path as the application itself.
func seedFixture(app core.App) error {
	fixture, err := loadFixture()
	if err != nil {
		return err
	}

	// drop the collections created by the system migrations so that the
	// fixture is the single source of truth
	existing, err := app.FindAllCollections()
	if err != nil {
		return err
	}
	for _, c := range existing {
		if c.IsView() {
			if err := app.DeleteView(c.Name); err != nil {
				return fmt.Errorf("failed to drop view %s: %w", c.Name, err)
			}
		} else if _, err := app.DB().NewQuery(fmt.Sprintf("DROP TABLE IF EXISTS {{%s}} CASCADE", c.Name)).Execute(); err != nil {
			return fmt.Errorf("failed to drop table %s: %w", c.Name, err)
		}
	}

	for _, table := range []string{"_collections", "_params"} {
		if _, err := app.DB().NewQuery(fmt.Sprintf("DELETE FROM {{%s}}", table)).Execute(); err != nil {
			return fmt.Errorf("failed to clear %s: %w", table, err)
		}
	}

	// insert the raw collection rows
	for _, row := range fixture.Collections {
		if _, err := app.DB().Insert("_collections", toParams(row, "system")).Execute(); err != nil {
			return fmt.Errorf("failed to insert collection %v: %w", row["name"], err)
		}
	}

	for _, row := range fixture.Params {
		if _, err := app.DB().Insert("_params", toParams(row)).Execute(); err != nil {
			return fmt.Errorf("failed to insert param: %w", err)
		}
	}

	if err := app.ReloadCachedCollections(); err != nil {
		return err
	}

	collections, err := app.FindAllCollections()
	if err != nil {
		return err
	}

	// create the record tables first so that relations and views can resolve
	for _, c := range collections {
		if c.IsView() {
			continue
		}
		if err := app.SyncRecordTableSchema(c, nil); err != nil {
			return fmt.Errorf("failed to sync table %s: %w", c.Name, err)
		}
	}

	// insert the record rows
	for _, c := range collections {
		if c.IsView() {
			continue
		}
		for _, row := range fixture.Records[c.Name] {
			params, err := recordParams(c, row)
			if err != nil {
				return fmt.Errorf("failed to prepare row for %s: %w", c.Name, err)
			}
			if len(params) == 0 {
				continue
			}
			if _, err := app.DB().Insert(c.Name, params).Execute(); err != nil {
				return fmt.Errorf("failed to insert row into %s: %w", c.Name, err)
			}
		}
	}

	// views are created last since they select from the record tables
	for _, c := range collections {
		if !c.IsView() {
			continue
		}
		query := c.ViewQuery
		if query == "" {
			continue
		}
		if err := app.SaveView(c.Name, query); err != nil {
			return fmt.Errorf("failed to create view %s: %w", c.Name, err)
		}
	}

	// the fixture may carry columns from older _logs revisions, so it is
	// narrowed down to the ones the current schema declares
	logColumns := []string{"id", "level", "message", "data", "created"}
	for _, row := range fixture.Logs {
		params := dbx.Params{}
		for _, col := range logColumns {
			if v, ok := row[col]; ok {
				params[col] = v
			}
		}
		if len(params) == 0 {
			continue
		}
		if _, err := app.AuxDB().Insert("_logs", params).Execute(); err != nil {
			return fmt.Errorf("failed to insert log: %w", err)
		}
	}

	if err := app.ReloadCachedCollections(); err != nil {
		return err
	}

	// the settings row was replaced after the app had already loaded the
	// bootstrap defaults, so the in-memory copy has to be refreshed
	return app.ReloadSettings()
}

// toParams converts a raw fixture row into insert params.
//
// boolColumns lists the columns that Postgres declares as BOOLEAN but that
// SQLite stored as 0/1 integers.
func toParams(row map[string]any, boolColumns ...string) dbx.Params {
	params := make(dbx.Params, len(row))

	for k, v := range row {
		if slices.Contains(boolColumns, k) {
			params[k] = toBool(v)
		} else {
			params[k] = v
		}
	}

	return params
}

// recordParams converts a raw fixture row into insert params, coercing the
// values that SQLite stored untyped into what the Postgres column expects.
func recordParams(collection *core.Collection, row map[string]any) (dbx.Params, error) {
	params := make(dbx.Params, len(row))

	for k, v := range row {
		field := collection.Fields.GetByName(k)
		if field == nil {
			// not part of the collection schema (eg. the internal rowid column)
			continue
		}

		switch field.Type() {
		case core.FieldTypeBool:
			params[k] = toBool(v)
		case core.FieldTypeJSON:
			params[k] = toJSONText(v)
		default:
			if v == nil {
				params[k] = nil
			} else {
				params[k] = v
			}
		}
	}

	return params, nil
}

func toBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case int64:
		return val != 0
	case string:
		return val == "1" || strings.EqualFold(val, "true")
	default:
		return false
	}
}

// toJSONText normalizes a fixture value into text that Postgres can cast to jsonb.
func toJSONText(v any) any {
	if v == nil {
		return nil
	}

	if s, ok := v.(string); ok {
		if s == "" {
			return nil
		}
		return s
	}

	encoded, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	return string(encoded)
}
