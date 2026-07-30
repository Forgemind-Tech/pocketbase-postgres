package dbutils

import (
	"regexp"
	"strings"

	"github.com/pocketbase/pocketbase/tools/tokenizer"
)

var (
	// note: the optional "USING <method>" clause is matched so that index
	// definitions read back from Postgres (pg_indexes.indexdef) can be parsed
	indexRegex       = regexp.MustCompile(`(?i)create\s+(unique\s+)?\s*index\s*(if\s+not\s+exists\s+)?(\S*)\s+on\s+(\S*)\s*(?:using\s+\S+\s*)?\(([\s\S]*?)\)(?:\s*where\s+([\s\S]*?))?\s*$`)
	indexColumnRegex = regexp.MustCompile(`(?im)^([\s\S]+?)(?:\s+collate\s+([\w]+))?(?:\s+(asc|desc))?$`)
)

// IndexColumn represents a single parsed SQL index column.
type IndexColumn struct {
	Name    string `json:"name"` // identifier or expression
	Collate string `json:"collate"`
	Sort    string `json:"sort"`
}

// Index represents a single parsed SQL CREATE INDEX expression.
type Index struct {
	SchemaName string        `json:"schemaName"`
	IndexName  string        `json:"indexName"`
	TableName  string        `json:"tableName"`
	Where      string        `json:"where"`
	Columns    []IndexColumn `json:"columns"`
	Unique     bool          `json:"unique"`
	Optional   bool          `json:"optional"`
}

// backtickIdentifierRegex matches a backtick quoted SQL identifier.
var backtickIdentifierRegex = regexp.MustCompile("`([^`]*)`")

// normalizeBacktickIdentifiers rewrites backtick quoted identifiers into
// their Postgres double quoted form.
func normalizeBacktickIdentifiers(expr string) string {
	return backtickIdentifierRegex.ReplaceAllString(expr, `"$1"`)
}

// lowerExprRegex matches a LOWER() call wrapping a single simple identifier.
var lowerExprRegex = regexp.MustCompile(`(?i)^lower\s*\(\s*([^()]+?)\s*\)$`)

// unwrapLowerExpr returns the identifier inside a LOWER() call.
func unwrapLowerExpr(expr string) (string, bool) {
	m := lowerExprRegex.FindStringSubmatch(strings.TrimSpace(expr))
	if len(m) != 2 {
		return "", false
	}

	return m[1], true
}

// isNocaseCollate checks whether the provided collate name is the legacy
// SQLite case-insensitive collation.
func isNocaseCollate(collate string) bool {
	return strings.EqualFold(strings.TrimSpace(collate), "nocase")
}

// IsValid checks if the current Index contains the minimum required fields to be considered valid.
func (idx Index) IsValid() bool {
	return idx.IndexName != "" && idx.TableName != "" && len(idx.Columns) > 0
}

// Build returns a "CREATE INDEX" SQL string from the current index parts.
//
// Returns empty string if idx.IsValid() is false.
func (idx Index) Build() string {
	if !idx.IsValid() {
		return ""
	}

	var str strings.Builder

	str.WriteString("CREATE ")

	if idx.Unique {
		str.WriteString("UNIQUE ")
	}

	str.WriteString("INDEX ")

	if idx.Optional {
		str.WriteString("IF NOT EXISTS ")
	}

	if idx.SchemaName != "" {
		str.WriteString(`"`)
		str.WriteString(idx.SchemaName)
		str.WriteString(`".`)
	}

	str.WriteString(`"`)
	str.WriteString(idx.IndexName)
	str.WriteString(`" `)

	str.WriteString(`ON "`)
	str.WriteString(idx.TableName)
	str.WriteString(`" (`)

	if len(idx.Columns) > 1 {
		str.WriteString("\n  ")
	}

	var hasCol bool
	for _, col := range idx.Columns {
		trimmedColName := strings.TrimSpace(col.Name)
		if trimmedColName == "" {
			continue
		}

		if hasCol {
			str.WriteString(",\n  ")
		}

		var quotedColName string
		if strings.Contains(col.Name, "(") || strings.Contains(col.Name, " ") {
			// most likely an expression
			quotedColName = trimmedColName
		} else {
			// regular identifier
			quotedColName = `"` + trimmedColName + `"`
		}

		// Postgres has no case-insensitive collation equivalent to SQLite's
		// NOCASE, so it is expressed as a LOWER() functional index instead.
		// The auth identity lookups compare with LOWER() to match.
		if isNocaseCollate(col.Collate) {
			str.WriteString("LOWER(")
			str.WriteString(quotedColName)
			str.WriteString(")")
		} else {
			str.WriteString(quotedColName)

			if col.Collate != "" {
				str.WriteString(` COLLATE "`)
				str.WriteString(col.Collate)
				str.WriteString(`"`)
			}
		}

		if col.Sort != "" {
			str.WriteString(" ")
			str.WriteString(strings.ToUpper(col.Sort))
		}

		hasCol = true
	}

	if hasCol && len(idx.Columns) > 1 {
		str.WriteString("\n")
	}

	str.WriteString(")")

	if idx.Where != "" {
		str.WriteString(" WHERE ")
		// the predicate is passed through verbatim, so any backtick quoting
		// (legacy definitions or hand-written index SQL) is normalized here
		str.WriteString(normalizeBacktickIdentifiers(idx.Where))
	}

	return str.String()
}

// ParseIndex parses the provided "CREATE INDEX" SQL string into Index struct.
func ParseIndex(createIndexExpr string) Index {
	result := Index{}

	matches := indexRegex.FindStringSubmatch(createIndexExpr)
	if len(matches) != 7 {
		return result
	}

	trimChars := "`\"'[]\r\n\t\f\v "

	// Unique
	// ---
	result.Unique = strings.TrimSpace(matches[1]) != ""

	// Optional (aka. "IF NOT EXISTS")
	// ---
	result.Optional = strings.TrimSpace(matches[2]) != ""

	// SchemaName and IndexName
	// ---
	nameTk := tokenizer.NewFromString(matches[3])
	nameTk.Separators('.')

	nameParts, _ := nameTk.ScanAll()
	if len(nameParts) == 2 {
		result.SchemaName = strings.Trim(nameParts[0], trimChars)
		result.IndexName = strings.Trim(nameParts[1], trimChars)
	} else {
		result.IndexName = strings.Trim(nameParts[0], trimChars)
	}

	// TableName
	// ---
	// Postgres reports the table schema-qualified (eg. "public.posts"),
	// so only the last part is kept as the table name.
	tableTk := tokenizer.NewFromString(matches[4])
	tableTk.Separators('.')

	tableParts, _ := tableTk.ScanAll()
	if len(tableParts) > 0 {
		result.TableName = strings.Trim(tableParts[len(tableParts)-1], trimChars)
	} else {
		result.TableName = strings.Trim(matches[4], trimChars)
	}

	// Columns
	// ---
	columnsTk := tokenizer.NewFromString(matches[5])
	columnsTk.Separators(',')

	rawColumns, _ := columnsTk.ScanAll()

	result.Columns = make([]IndexColumn, 0, len(rawColumns))

	for _, col := range rawColumns {
		colMatches := indexColumnRegex.FindStringSubmatch(col)
		if len(colMatches) != 4 {
			continue
		}

		trimmedName := strings.Trim(colMatches[1], trimChars)
		if trimmedName == "" {
			continue
		}

		collate := strings.TrimSpace(colMatches[2])

		// A case-insensitive index is emitted as a LOWER() expression (see
		// Build), so it is normalized back into a plain column with the nocase
		// collate to keep the parse/build round-trip stable - callers such as
		// the auth identity lookups rely on finding the index by column name.
		if inner, ok := unwrapLowerExpr(trimmedName); ok {
			trimmedName = strings.Trim(inner, trimChars)
			collate = "NOCASE"
		}

		result.Columns = append(result.Columns, IndexColumn{
			Name:    trimmedName,
			Collate: collate,
			Sort:    strings.ToUpper(colMatches[3]),
		})
	}

	// WHERE expression
	// ---
	result.Where = strings.TrimSpace(matches[6])

	return result
}

// FindSingleColumnUniqueIndex returns the first matching single column unique index.
func FindSingleColumnUniqueIndex(indexes []string, column string) (Index, bool) {
	var index Index

	for _, idx := range indexes {
		index := ParseIndex(idx)
		if index.Unique && len(index.Columns) == 1 && strings.EqualFold(index.Columns[0].Name, column) {
			return index, true
		}
	}

	return index, false
}

// Deprecated: Use `_, ok := FindSingleColumnUniqueIndex(indexes, column)` instead.
//
// HasColumnUniqueIndex loosely checks whether the specified column has
// a single column unique index (WHERE statements are ignored).
func HasSingleColumnUniqueIndex(column string, indexes []string) bool {
	_, ok := FindSingleColumnUniqueIndex(indexes, column)
	return ok
}
