package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/schedule"
	"github.com/Mtrya/llmloot/internal/state"
	"github.com/Mtrya/llmloot/internal/target/kimicode"
)

func TestSyncStopsWhenOwnershipStateIsMissing(t *testing.T) {
	llmlootHome := t.TempDir()
	t.Setenv("LLMLOOT_HOME", llmlootHome)
	configuration := config.Default()
	if err := config.Save(filepath.Join(llmlootHome, "config.toml"), configuration); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"sync", "--dry-run"}, &stdout, &stderr); exitCode != 1 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ownership state is missing") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestSyncIfDueStopsBeforeDiscoveryWhenBoundaryIsSatisfied(t *testing.T) {
	llmlootHome := t.TempDir()
	t.Setenv("LLMLOOT_HOME", llmlootHome)
	configuration := config.Default()
	configPath := filepath.Join(llmlootHome, "config.toml")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	boundary, err := schedule.CurrentBoundary(time.Now().In(time.Local), configuration.Schedule.LocalTime)
	if err != nil {
		t.Fatal(err)
	}
	currentState.SatisfyScheduleBoundary(boundary)
	if err := state.Save(filepath.Join(llmlootHome, "state.json"), currentState); err != nil {
		t.Fatal(err)
	}
	lock, err := state.AcquireLock(config.LockPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	}()

	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"sync", "--if-due", "--json"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	var result struct {
		Outcome     string            `json:"outcome"`
		Jobs        []json.RawMessage `json:"jobs"`
		TargetPlans []json.RawMessage `json:"target_plans"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "not_due" || len(result.Jobs) != 0 || len(result.TargetPlans) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestSyncIfDueIsAQuietNoOpWhenSchedulingIsDisabled(t *testing.T) {
	llmlootHome := t.TempDir()
	t.Setenv("LLMLOOT_HOME", llmlootHome)
	configuration := config.Default()
	configuration.Schedule.Enabled = false
	if err := config.Save(filepath.Join(llmlootHome, "config.toml"), configuration); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(filepath.Join(llmlootHome, "state.json"), state.New()); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"sync", "--if-due", "--quiet"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestDoctorIsReadOnlyForAHealthyLocalConfiguration(t *testing.T) {
	llmlootHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("LLMLOOT_HOME", llmlootHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(llmlootHome, "config.toml")
	statePath := filepath.Join(llmlootHome, "state.json")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installation.ConfigPath, []byte("# valid empty user config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	currentState.Targets["kimi-code"] = state.TargetState{Path: installation.ConfigPath}
	if err := state.Save(statePath, currentState); err != nil {
		t.Fatal(err)
	}
	configBefore, _ := os.ReadFile(configPath)
	stateBefore, _ := os.ReadFile(statePath)
	targetBefore, _ := os.ReadFile(installation.ConfigPath)
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"doctor", "--json"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", exitCode, stderr.String(), stdout.String())
	}
	var result doctorResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "healthy" {
		t.Fatalf("doctor result = %#v", result)
	}
	assertFileBytes(t, configPath, configBefore)
	assertFileBytes(t, statePath, stateBefore)
	assertFileBytes(t, installation.ConfigPath, targetBefore)
}

func TestUninstallDryRunThenRemovalUsesOwnership(t *testing.T) {
	llmlootHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("LLMLOOT_HOME", llmlootHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(llmlootHome, "config.toml")
	statePath := filepath.Join(llmlootHome, "state.json")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	document, err := kimicode.Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := kimicode.ProviderForSource("openrouter")
	plan := kimicode.PlanSync(document, state.TargetState{}, []kimicode.ProviderRequirement{{Spec: provider, APIKey: "isolated-secret"}}, []kimicode.JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true, Models: []kimicode.ModelInput{{ID: "stealth/owned", DisplayName: "Owned", Source: "openrouter", Provider: "openrouter", Job: "openrouter-kimi-code", ContextWindow: 200000, OutputModalities: []string{"text"}}}}})
	if err := plan.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	currentState.Targets["kimi-code"] = plan.Ownership
	if err := state.Save(statePath, currentState); err != nil {
		t.Fatal(err)
	}
	configBefore, _ := os.ReadFile(configPath)
	stateBefore, _ := os.ReadFile(statePath)
	targetBefore, _ := os.ReadFile(installation.ConfigPath)
	var dryOutput, dryError bytes.Buffer
	if exitCode := Run(context.Background(), []string{"uninstall", "--dry-run", "--json"}, &dryOutput, &dryError); exitCode != 0 {
		t.Fatalf("dry-run exit code = %d, stderr = %s", exitCode, dryError.String())
	}
	assertFileBytes(t, configPath, configBefore)
	assertFileBytes(t, statePath, stateBefore)
	assertFileBytes(t, installation.ConfigPath, targetBefore)
	if strings.Contains(dryOutput.String(), "isolated-secret") {
		t.Fatal("credential appeared in dry-run output")
	}

	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"uninstall", "--yes"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("uninstall exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("llmloot config remains: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("llmloot state remains: %v", err)
	}
	targetAfter, _ := os.ReadFile(installation.ConfigPath)
	if strings.Contains(string(targetAfter), "stealth/owned") || strings.Contains(string(targetAfter), "providers.openrouter") {
		t.Fatal("owned target entries remain")
	}
}

func TestDoctorReportsAnExistingLockWithoutTouchingIt(t *testing.T) {
	llmlootHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("LLMLOOT_HOME", llmlootHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(llmlootHome, "config.toml")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installation.ConfigPath, []byte("# valid empty user config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	currentState.Targets["kimi-code"] = state.TargetState{Path: installation.ConfigPath}
	if err := state.Save(filepath.Join(llmlootHome, "state.json"), currentState); err != nil {
		t.Fatal(err)
	}
	lockPath := config.LockPath(configPath)
	if err := os.WriteFile(lockPath, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"doctor", "--json"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", exitCode, stderr.String(), stdout.String())
	}
	var result doctorResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range result.Checks {
		if check.Name == "process_lock" {
			found = true
			if check.Status != "warning" || check.Remediation == "" {
				t.Fatalf("process_lock check = %#v", check)
			}
		}
	}
	if !found {
		t.Fatalf("no process_lock check in %#v", result.Checks)
	}
	assertFileBytes(t, lockPath, []byte("999999\n"))
}

func localOnlyConfiguration() config.Config {
	configuration := config.Default()
	configuration.Schedule.Enabled = false
	for name, job := range configuration.Jobs {
		job.Enabled = false
		configuration.Jobs[name] = job
	}
	return configuration
}

func cliKimi(t *testing.T, home string) kimicode.Installation {
	t.Helper()
	binary, err := exec.LookPath("kimi")
	if err != nil {
		t.Skip("real Kimi Code binary is not installed")
	}
	installation, err := kimicode.InspectInstallation(context.Background(), binary, home)
	if err != nil {
		t.Skipf("supported Kimi Code binary is unavailable: %v", err)
	}
	return installation
}

func assertFileBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("file %s changed", path)
	}
}
