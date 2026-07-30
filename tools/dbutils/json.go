package dbutils

import (
	"fmt"
	"regexp"
	"strings"
)

// jsonPathSegmentRegex splits a SQLite-style json path into its
// object key and array index segments (eg. `a.b[0].c` -> a, b, 0, c).
var jsonPathSegmentRegex = regexp.MustCompile(`\[(\d+)\]|([^.\[\]]+)`)

// normalizedJSONB returns an expression that coerces the provided column
// into a jsonb array, mirroring the old SQLite json_array() normalization
// for columns that don't hold a json array value.
func normalizedJSONB(column string) string {
	return fmt.Sprintf(
		`(CASE WHEN [[%s]]::text IS JSON ARRAY THEN [[%s]]::jsonb ELSE jsonb_build_array([[%s]]) END)`,
		column, column, column,
	)
}

// JSONEach returns a set-returning jsonb expression with some
// normalizations for non-json columns.
//
// The resulting relation exposes a single "value" column, matching the
// column name that jsonb_array_elements_text produces by default.
func JSONEach(column string) string {
	return "jsonb_array_elements_text" + normalizedJSONB(column)
}

// JSONArrayLength returns a jsonb_array_length expression
// with some normalizations for non-json columns.
//
// It works with both json and non-json column values.
//
// Returns 0 for empty string or NULL column values.
func JSONArrayLength(column string) string {
	return fmt.Sprintf(
		`jsonb_array_length(CASE WHEN [[%s]]::text IS JSON ARRAY THEN [[%s]]::jsonb WHEN [[%s]] IS NULL OR [[%s]]::text = '' THEN '[]'::jsonb ELSE jsonb_build_array([[%s]]) END)`,
		column, column, column, column, column,
	)
}

// jsonPathToPgLiteral converts a SQLite-style json path (eg. `a.b[0]`)
// into a Postgres path array literal (eg. `{a,b,0}`).
//
// An empty path yields `{}`, which extracts the whole value.
func jsonPathToPgLiteral(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "{}"
	}

	matches := jsonPathSegmentRegex.FindAllStringSubmatch(path, -1)

	segments := make([]string, 0, len(matches))
	for _, m := range matches {
		seg := m[1] // array index
		if seg == "" {
			seg = m[2] // object key
		}
		if seg == "" {
			continue
		}
		// quote the segment so that separators inside keys can't break out
		// of the array literal
		segments = append(segments, `"`+strings.ReplaceAll(seg, `"`, `""`)+`"`)
	}

	return "{" + strings.Join(segments, ",") + "}"
}

// JSONExtract returns a jsonb extraction expression with
// some normalizations for non-json columns.
//
// The value is returned as text (Postgres #>> semantics).
func JSONExtract(column string, path string) string {
	pgPath := jsonPathToPgLiteral(path)

	return fmt.Sprintf(
		// note: to_jsonb() handles the cases where the extraction is used
		// against a non-json column
		`(CASE WHEN [[%s]]::text IS JSON THEN ([[%s]]::jsonb #>> '%s') ELSE (to_jsonb([[%s]]) #>> '%s') END)`,
		column,
		column,
		pgPath,
		column,
		pgPath,
	)
}
