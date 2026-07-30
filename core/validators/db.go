package validators

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pocketbase/dbx"
	validation "github.com/pocketbase/ozzo-validation/v4"
)

// UniqueId checks whether a field string id already exists in the specified table.
//
// Example:
//
//	validation.Field(&form.RelId, validation.By(validators.UniqueId(form.app.DB(), "tbl_example"))
func UniqueId(db dbx.Builder, tableName string) validation.RuleFunc {
	return func(value any) error {
		v, _ := value.(string)
		if v == "" {
			return nil // nothing to check
		}

		var foundId string

		err := db.
			Select("id").
			From(tableName).
			Where(dbx.HashExp{"id": v}).
			Limit(1).
			Row(&foundId)

		if (err != nil && !errors.Is(err, sql.ErrNoRows)) || foundId != "" {
			return validation.NewError("validation_invalid_or_existing_id", "The model id is invalid or already exists.")
		}

		return nil
	}
}

// NormalizeUniqueIndexError attempts to convert a
// unique constraint violation into a validation.Errors.
//
// The provided err is returned as it is without changes if:
// - err is nil
// - err is already validation.Errors
// - err is not a unique constraint violation
func NormalizeUniqueIndexError(err error, tableOrAlias string, fieldNames []string) error {
	if err == nil {
		return err
	}

	if _, ok := err.(validation.Errors); ok {
		return err
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgUniqueViolationCode {
		return err
	}

	// the violation must come from the expected table
	if pgErr.TableName != "" && !strings.EqualFold(pgErr.TableName, tableOrAlias) {
		return err
	}

	conflicting := uniqueViolationColumns(pgErr)
	if len(conflicting) == 0 {
		return err
	}

	normalizedErrs := validation.Errors{}

	for _, name := range fieldNames {
		if _, ok := conflicting[strings.ToLower(name)]; ok {
			normalizedErrs[name] = validation.NewError("validation_not_unique", "Value must be unique")
		}
	}

	if len(normalizedErrs) > 0 {
		return normalizedErrs
	}

	return err
}

// pgUniqueViolationCode is the Postgres SQLSTATE for a unique constraint violation.
const pgUniqueViolationCode = "23505"

// uniqueViolationDetailRegex extracts the column list out of the error detail
// (eg. `Key (email, tokenKey)=(a, b) already exists.`).
var uniqueViolationDetailRegex = regexp.MustCompile(`(?i)^key \(([^)]+)\)`)

// uniqueViolationColumns returns the lowercased columns reported by a unique
// constraint violation.
//
// Postgres names the violated index rather than the columns in the message, so
// the structured detail is used and the index name is only a fallback (it is
// not guaranteed to embed the column names).
func uniqueViolationColumns(pgErr *pgconn.PgError) map[string]struct{} {
	result := map[string]struct{}{}

	if m := uniqueViolationDetailRegex.FindStringSubmatch(strings.TrimSpace(pgErr.Detail)); len(m) == 2 {
		for _, col := range strings.Split(m[1], ",") {
			col = strings.ToLower(strings.Trim(strings.TrimSpace(col), `"`))
			if col != "" {
				result[col] = struct{}{}
			}
		}
	}

	return result
}
