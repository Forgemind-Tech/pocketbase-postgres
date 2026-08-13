package core

import "sync"

// deferredDeletes holds file deletions postponed until the running backup ends.
//
// A backup archive contains two things that must agree with each other: the SQL
// dump, taken at the start, and the files in pb_data, archived a moment later.
// If a file is physically deleted between the two, the restored database still
// references it and the attachment is broken.
//
// Postponing the deletions removes that failure mode without blocking anything.
// The opposite skew - a file present in the archive whose record is not in the
// dump - is harmless, and file deletion here is already best-effort ("optimistic
// delete", failures logged and ignored), so delaying it fits the existing
// contract.
type deferredDeletes struct {
	mu  sync.Mutex
	ops []func()
}

// DeferFileDeleteDuringBackup postpones op until the running backup finishes.
//
// It reports whether op was postponed; when it returns false the caller must
// perform the deletion itself, as usual.
func (app *BaseApp) DeferFileDeleteDuringBackup(op func()) bool {
	app.deferredDeletes.mu.Lock()
	defer app.deferredDeletes.mu.Unlock()

	// the lock is also held while the backup window is closed, so the flag
	// cannot be cleared between this check and the append
	if !app.Store().Has(StoreKeyActiveBackup) {
		return false
	}

	app.deferredDeletes.ops = append(app.deferredDeletes.ops, op)

	return true
}

// endBackupWindow clears the active backup marker and runs everything that was
// postponed while it was set.
//
// Clearing the flag and draining the queue happen under the same lock, so a
// deletion racing the end of a backup either runs immediately or is drained
// here - it cannot be enqueued into a queue nobody will look at again.
func (app *BaseApp) endBackupWindow() {
	app.deferredDeletes.mu.Lock()
	app.Store().Remove(StoreKeyActiveBackup)
	ops := app.deferredDeletes.ops
	app.deferredDeletes.ops = nil
	app.deferredDeletes.mu.Unlock()

	for _, op := range ops {
		op()
	}
}
