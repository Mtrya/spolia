package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingStateIsAnEmptyValidState(t *testing.T) {
	t.Parallel()
	loaded, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d", loaded.SchemaVersion)
	}
}

func TestCorruptStateFails(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("corrupt state was accepted")
	}
}

func TestStateWithTrailingDataFails(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1} trailing`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("state with trailing data was accepted")
	}
}

func TestProcessLockIsExclusiveAndRemoved(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "llmloot.lock")
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path); err == nil {
		t.Fatal("second lock acquisition succeeded")
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock remains after release: %v", err)
	}
}
