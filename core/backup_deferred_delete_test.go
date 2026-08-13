package core

// note: an internal test so that it can reach endBackupWindow - the deferral
// logic touches only the in-memory store, so no bootstrapped app is needed
// (and "tests" imports "core", which would make an external test a cycle)

import (
	"sync"
	"testing"
)

func TestDeferFileDeleteDuringBackup(t *testing.T) {
	app := NewBaseApp(BaseAppConfig{DataDir: t.TempDir()})

	// with no backup running the caller must delete as usual
	if app.DeferFileDeleteDuringBackup(func() { t.Fatal("must not run") }) {
		t.Fatal("expected the deletion not to be postponed outside a backup")
	}

	// simulate a backup window
	app.Store().Set(StoreKeyActiveBackup, "test.zip")

	var mu sync.Mutex
	ran := 0
	for i := 0; i < 3; i++ {
		postponed := app.DeferFileDeleteDuringBackup(func() {
			mu.Lock()
			defer mu.Unlock()
			ran++
		})
		if !postponed {
			t.Fatalf("expected deletion %d to be postponed during a backup", i)
		}
	}

	if ran != 0 {
		t.Fatalf("expected nothing to run while the backup is active, got %d", ran)
	}

	// closing the window must clear the marker and drain the queue
	app.endBackupWindow()

	if ran != 3 {
		t.Fatalf("expected all 3 postponed deletions to run, got %d", ran)
	}

	if app.Store().Has(StoreKeyActiveBackup) {
		t.Fatal("expected the active backup marker to be cleared")
	}

	// draining twice must not re-run anything
	app.endBackupWindow()
	if ran != 3 {
		t.Fatalf("expected the queue to be drained once, got %d", ran)
	}

	// and deletions after the window must run inline again
	if app.DeferFileDeleteDuringBackup(func() { t.Fatal("must not run") }) {
		t.Fatal("expected deletions to run inline once the backup finished")
	}
}

func TestDeferFileDeleteDuringBackupIsSharedWithClones(t *testing.T) {
	app := NewBaseApp(BaseAppConfig{DataDir: t.TempDir()})

	// BaseApp is shallow-copied to build the transaction and no-hooks clones.
	// If the queue were a value rather than a pointer, each clone would get its
	// own and anything enqueued through it would silently never run.
	clone := app.UnsafeWithoutHooks()

	app.Store().Set(StoreKeyActiveBackup, "test.zip")

	ran := 0
	if !clone.DeferFileDeleteDuringBackup(func() { ran++ }) {
		t.Fatal("expected the clone to see the active backup marker")
	}

	// draining on the original must run what the clone enqueued
	app.endBackupWindow()

	if ran != 1 {
		t.Fatalf("expected the clone's deletion to be drained by the parent, got %d", ran)
	}
}
