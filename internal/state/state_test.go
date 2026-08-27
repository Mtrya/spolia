package state

import (
	"bytes"
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

func TestSaveRoundTripsOwnershipWithoutCredentialMaterial(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	current := New()
	current.LLMlootVersion = "test"
	current.Targets["kimi-code"] = TargetState{
		Path: "/isolated/config.toml",
		Providers: map[string]ProviderOwnership{
			"openrouter": {Fields: map[string]string{"type": `"openai"`, "base_url": `"https://openrouter.ai/api/v1"`}},
		},
		Models: map[string]ModelOwnership{
			"stealth/example": {Source: "openrouter", Job: "openrouter-kimi-code", Fields: map[string]string{"model": `"stealth/example"`}},
		},
	}
	if err := Save(path, current); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("api_key")) || bytes.Contains(contents, []byte("secret")) {
		t.Fatalf("state contains credential material: %s", contents)
	}
	loaded, exists, err := Read(path)
	if err != nil || !exists {
		t.Fatalf("read state: exists=%t err=%v", exists, err)
	}
	if loaded.Targets["kimi-code"].Models["stealth/example"].Job != "openrouter-kimi-code" {
		t.Fatalf("loaded state = %#v", loaded)
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
