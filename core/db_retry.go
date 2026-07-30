package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pocketbase/dbx"
)

// default retries intervals (in ms)
var defaultRetryIntervals = []int{50, 100, 150, 200, 300, 400, 500, 700, 1000}

// default max retry attempts
const defaultMaxLockRetries = 12

func execLockRetry(timeout time.Duration, maxRetries int) dbx.ExecHookFunc {
	return func(q *dbx.Query, op func() error) error {
		if q.Context() == nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer func() {
				cancel()
				//nolint:staticcheck
				q.WithContext(nil) // reset
			}()
			q.WithContext(cancelCtx)
		}

		execErr := baseLockRetry(func(attempt int) error {
			return op()
		}, maxRetries)
		if execErr != nil && !errors.Is(execErr, sql.ErrNoRows) {
			execErr = fmt.Errorf("%w; failed query: %s", execErr, q.SQL())
		}

		return execErr
	}
}

func baseLockRetry(op func(attempt int) error, maxRetries int) error {
	attempt := 1

Retry:
	err := op(attempt)

	if err != nil && attempt <= maxRetries && isRetryablePgError(err) {
		// wait and retry
		time.Sleep(getDefaultRetryInterval(attempt))
		attempt++
		goto Retry
	}

	return err
}

// retryablePgErrorCodes are the Postgres SQLSTATE codes for transient
// concurrency failures where re-running the same statement may succeed.
var retryablePgErrorCodes = []string{
	"40001", // serialization_failure
	"40P01", // deadlock_detected
	"55P03", // lock_not_available
	"55006", // object_in_use
}

// isRetryablePgError checks whether the provided error is a transient
// Postgres concurrency error that is safe to retry.
func isRetryablePgError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return slices.Contains(retryablePgErrorCodes, pgErr.Code)
}

func getDefaultRetryInterval(attempt int) time.Duration {
	if attempt < 0 || attempt > len(defaultRetryIntervals)-1 {
		return time.Duration(defaultRetryIntervals[len(defaultRetryIntervals)-1]) * time.Millisecond
	}

	return time.Duration(defaultRetryIntervals[attempt]) * time.Millisecond
}
