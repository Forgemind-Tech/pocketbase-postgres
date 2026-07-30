package dbutils_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/tools/dbutils"
)

func TestJSONEach(t *testing.T) {
	result := dbutils.JSONEach("a.b")

	expected := "jsonb_array_elements_text(CASE WHEN [[a.b]]::text IS JSON ARRAY THEN [[a.b]]::jsonb ELSE jsonb_build_array([[a.b]]) END)"

	if result != expected {
		t.Fatalf("Expected\n%v\ngot\n%v", expected, result)
	}
}

func TestJSONArrayLength(t *testing.T) {
	result := dbutils.JSONArrayLength("a.b")

	expected := "jsonb_array_length(CASE WHEN [[a.b]]::text IS JSON ARRAY THEN [[a.b]]::jsonb WHEN [[a.b]] IS NULL OR [[a.b]]::text = '' THEN '[]'::jsonb ELSE jsonb_build_array([[a.b]]) END)"

	if result != expected {
		t.Fatalf("Expected\n%v\ngot\n%v", expected, result)
	}
}

func TestJSONExtract(t *testing.T) {
	scenarios := []struct {
		name     string
		column   string
		path     string
		expected string
	}{
		{
			"empty path",
			"a.b",
			"",
			`(CASE WHEN [[a.b]]::text IS JSON THEN ([[a.b]]::jsonb #>> '{}') ELSE (to_jsonb([[a.b]]) #>> '{}') END)`,
		},
		{
			"starting with array index",
			"a.b",
			"[1].a[2]",
			`(CASE WHEN [[a.b]]::text IS JSON THEN ([[a.b]]::jsonb #>> '{"1","a","2"}') ELSE (to_jsonb([[a.b]]) #>> '{"1","a","2"}') END)`,
		},
		{
			"starting with key",
			"a.b",
			"a.b[2].c",
			`(CASE WHEN [[a.b]]::text IS JSON THEN ([[a.b]]::jsonb #>> '{"a","b","2","c"}') ELSE (to_jsonb([[a.b]]) #>> '{"a","b","2","c"}') END)`,
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			result := dbutils.JSONExtract(s.column, s.path)

			if result != s.expected {
				t.Fatalf("Expected\n%v\ngot\n%v", s.expected, result)
			}
		})
	}
}
