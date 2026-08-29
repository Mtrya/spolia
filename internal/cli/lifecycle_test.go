package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mtrya/spolia/internal/app"
	"github.com/Mtrya/spolia/internal/config"
	"github.com/Mtrya/spolia/internal/schedule"
	"github.com/Mtrya/spolia/internal/state"
	"github.com/Mtrya/spolia/internal/target/kimicode"
)

func TestSyncStopsWhenOwnershipStateIsMissing(t *testing.T) {
	spoliaHome := t.TempDir()
	t.Setenv("SPOLIA_HOME", spoliaHome)
	configuration := config.Default()
	if err := config.Save(filepath.Join(spoliaHome, "config.toml"), configuration); err != nil {
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
	spoliaHome := t.TempDir()
	t.Setenv("SPOLIA_HOME", spoliaHome)
	configuration := config.Default()
	configPath := filepath.Join(spoliaHome, "config.toml")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	boundary, err := schedule.CurrentBoundary(time.Now().In(time.Local), configuration.Schedule.LocalTime)
	if err != nil {
		t.Fatal(err)
	}
	currentState.SatisfyScheduleBoundary(boundary)
	if err := state.Save(filepath.Join(spoliaHome, "state.json"), currentState); err != nil {
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
	spoliaHome := t.TempDir()
	t.Setenv("SPOLIA_HOME", spoliaHome)
	configuration := config.Default()
	configuration.Schedule.Enabled = false
	if err := config.Save(filepath.Join(spoliaHome, "config.toml"), configuration); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(filepath.Join(spoliaHome, "state.json"), state.New()); err != nil {
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
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(spoliaHome, "config.toml")
	statePath := filepath.Join(spoliaHome, "state.json")
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
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(spoliaHome, "config.toml")
	statePath := filepath.Join(spoliaHome, "state.json")
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
		t.Fatalf("spolia config remains: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("spolia state remains: %v", err)
	}
	targetAfter, _ := os.ReadFile(installation.ConfigPath)
	if strings.Contains(string(targetAfter), "stealth/owned") || strings.Contains(string(targetAfter), "providers.openrouter") {
		t.Fatal("owned target entries remain")
	}
}

func TestDoctorReportsAnExistingLockWithoutTouchingIt(t *testing.T) {
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(spoliaHome, "config.toml")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installation.ConfigPath, []byte("# valid empty user config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	currentState.Targets["kimi-code"] = state.TargetState{Path: installation.ConfigPath}
	if err := state.Save(filepath.Join(spoliaHome, "state.json"), currentState); err != nil {
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

func TestSyncBeforeSetupPointsToSetup(t *testing.T) {
	t.Setenv("SPOLIA_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"sync"}, &stdout, &stderr); exitCode != 1 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "run spolia setup") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestDoctorWelcomesAnUnconfiguredInstallation(t *testing.T) {
	if _, err := exec.LookPath("kimi"); err != nil {
		t.Skip("real Kimi Code binary is not installed")
	}
	t.Setenv("SPOLIA_HOME", t.TempDir())
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"doctor", "--json"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", exitCode, stderr.String(), stdout.String())
	}
	var result doctorResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "not_configured" {
		t.Fatalf("doctor outcome = %q", result.Outcome)
	}
	for _, check := range result.Checks {
		if check.Name == "ownership_state" {
			t.Fatalf("first-run doctor reported ownership state: %#v", check)
		}
	}
}

func TestUninstallBeforeSetupHasNothingToRemove(t *testing.T) {
	t.Setenv("SPOLIA_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"uninstall", "--yes"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "nothing to remove") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestUninstallCleansUpWithAnUnreadableConfiguration(t *testing.T) {
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(spoliaHome, "config.toml")
	statePath := filepath.Join(spoliaHome, "state.json")
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
	if err := os.WriteFile(configPath, []byte("schema_version = [[[\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"uninstall", "--yes"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("uninstall exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "configuration is unreadable") {
		t.Fatalf("degraded cleanup was not announced: stderr = %s", stderr.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("spolia config remains: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("spolia state remains: %v", err)
	}
	targetAfter, _ := os.ReadFile(installation.ConfigPath)
	if strings.Contains(string(targetAfter), "stealth/owned") || strings.Contains(string(targetAfter), "providers.openrouter") {
		t.Fatal("owned target entries remain")
	}
}

func TestSetupYesSkipsSourcesWithoutCredentials(t *testing.T) {
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("ZENMUX_API_KEY", "")
	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"setup", "--yes"}, &stdout, &stderr); exitCode != 1 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "skipping openrouter") || !strings.Contains(stderr.String(), "skipping zenmux") {
		t.Fatalf("uncredentialed sources were not skipped: stderr = %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "OPENROUTER_API_KEY") {
		t.Fatalf("final error does not name the remedy: stderr = %s", stderr.String())
	}
}

func TestSetupYesSucceedsWithOneCredentialAndSchedulerWarning(t *testing.T) {
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	t.Setenv("ZENMUX_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	fixture, err := os.ReadFile(filepath.Join("..", "testdata", "openrouter-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(fixture)
	}))
	defer server.Close()
	t.Setenv("SPOLIA_TEST_OPENROUTER_MODELS_ENDPOINT", server.URL)

	var stdout, stderr bytes.Buffer
	if exitCode := Run(context.Background(), []string{"setup", "--yes", "--json"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", exitCode, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "skipping zenmux") {
		t.Fatalf("zenmux was not skipped: stderr = %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "test-key") {
		t.Fatal("credential appeared in output")
	}
	var result app.SyncResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "success" {
		t.Fatalf("outcome = %q", result.Outcome)
	}
	if result.Schedule == nil || result.Schedule.Status != "error" || !strings.Contains(result.Schedule.Error, "SPOLIA_HOME") {
		t.Fatalf("scheduler failure was not reported as a non-fatal warning: %#v", result.Schedule)
	}
	saved, err := config.Load(filepath.Join(spoliaHome, "config.toml"))
	if err != nil {
		t.Fatalf("setup did not persist the configuration: %v", err)
	}
	if saved.Jobs["zenmux-kimi-code"].Enabled {
		t.Fatal("the uncredentialed zenmux job stayed enabled in the saved configuration")
	}
}

func TestDoctorReportsManagedModelsAndSchedule(t *testing.T) {
	spoliaHome := t.TempDir()
	kimiHome := t.TempDir()
	installation := cliKimi(t, kimiHome)
	t.Setenv("SPOLIA_HOME", spoliaHome)
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	configuration := localOnlyConfiguration()
	configPath := filepath.Join(spoliaHome, "config.toml")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installation.ConfigPath, []byte("# valid empty user config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	document, err := kimicode.Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := kimicode.ProviderForSource("openrouter")
	plan := kimicode.PlanSync(document, state.TargetState{}, []kimicode.ProviderRequirement{{Spec: provider, APIKey: "isolated-secret"}}, []kimicode.JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true, Models: []kimicode.ModelInput{{ID: "stealth/example", DisplayName: "Example", Source: "openrouter", Provider: "openrouter", Job: "openrouter-kimi-code", ContextWindow: 200000, OutputModalities: []string{"text"}}}}})
	if err := plan.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	currentState.Targets["kimi-code"] = plan.Ownership
	currentState.RecordJob("openrouter-kimi-code", "selected", []string{"stealth/example"}, "", time.Now())
	if err := state.Save(filepath.Join(spoliaHome, "state.json"), currentState); err != nil {
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
	if result.Status == nil || len(result.Status.Models) != 1 || result.Status.Models[0].ID != "stealth/example" {
		t.Fatalf("status models = %#v", result.Status)
	}
	if len(result.Status.Jobs) != 1 || result.Status.Jobs[0].Outcome != "selected" || result.Status.Jobs[0].LastAttempt == nil {
		t.Fatalf("status jobs = %#v", result.Status.Jobs)
	}
	if result.Status.Scheduler == nil || result.Status.Scheduler.Enabled {
		t.Fatalf("status scheduler = %#v", result.Status.Scheduler)
	}
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
