package kimicode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mtrya/spolia/internal/state"
)

func TestRealKimiValidatesLifecycleAndPreservesUnrelatedContent(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	initial := `# user comment
default_model = "user/default"

[providers.user]
type = "openai"
base_url = "https://example.invalid/v1"
api_key = "user-secret"
custom_headers = { X-User = "kept" }

[models."user/default"]
provider = "user"
model = "user/default"
max_context_size = 131072
capabilities = ["tool_use"]
`
	if err := os.WriteFile(installation.ConfigPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := ProviderForSource("openrouter")
	selection := successfulSelection(modelInput("stealth/alpha", "openrouter-kimi-code", "openrouter", 200000))
	plan := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "bootstrap-secret"}}, []JobSelection{selection})
	if len(plan.Conflicts) != 0 || !plan.Write {
		t.Fatalf("plan = %#v", plan)
	}
	if err := plan.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, preserved := range []string{"# user comment", `default_model = "user/default"`, `custom_headers = { X-User = "kept" }`} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("missing preserved content %q", preserved)
		}
	}
	for _, generated := range []string{`[providers.openrouter]`, `[models."stealth/alpha"]`, `provider = "openrouter"`, `model = "stealth/alpha"`, `capabilities = ["thinking", "tool_use"]`} {
		if !strings.Contains(text, generated) {
			t.Fatalf("missing generated content %q", generated)
		}
	}

	reloaded, err := Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	idempotent := PlanSync(reloaded, plan.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{selection})
	if len(idempotent.Conflicts) != 0 || idempotent.Write || len(idempotent.Changes) != 0 {
		t.Fatalf("idempotent plan = %#v", idempotent)
	}

	withOverride := strings.Replace(text, `[models."stealth/alpha"]`, "# model note\n"+`[models."stealth/alpha"]`, 1)
	withOverride = strings.Replace(withOverride, `display_name = "Temporary Model"`, "display_name = \"Temporary Model\"\nuser_note = \"keep\"", 1) + `
[models."stealth/alpha".overrides]
max_output_size = 1024
`
	withOverride = strings.Replace(withOverride, "max_context_size = 200000", `"max_context_size" = 200000 # context note`, 1)
	withOverride = strings.Replace(withOverride, `capabilities = ["thinking", "tool_use"]`, "capabilities = [\n  \"thinking\",\n  \"tool_use\",\n]", 1)
	if err := os.WriteFile(installation.ConfigPath, []byte(withOverride), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err = Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	updatedInput := modelInput("stealth/alpha", "openrouter-kimi-code", "openrouter", 262144)
	updatedInput.Capabilities = map[string]bool{}
	updated := PlanSync(reloaded, plan.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(updatedInput)})
	if len(updated.Conflicts) != 0 || !updated.Write {
		t.Fatalf("update plan = %#v", updated)
	}
	if err := updated.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	afterUpdate, _ := os.ReadFile(installation.ConfigPath)
	if !strings.Contains(string(afterUpdate), "# model note") || !strings.Contains(string(afterUpdate), `user_note = "keep"`) || !strings.Contains(string(afterUpdate), `"max_context_size" = 262144 # context note`) || !strings.Contains(string(afterUpdate), `capabilities = ["tool_use"]`) || !strings.Contains(string(afterUpdate), `[models."stealth/alpha".overrides]`) {
		t.Fatal("unrelated model additions were not preserved")
	}
}

func TestRelevantEditConflictsButManualAliasIsNeverOverwritten(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	provider, _ := ProviderForSource("openrouter")
	initial := `[providers.openrouter]
type = "openai"
base_url = "https://openrouter.ai/api/v1"
api_key = "secret"

[models."manual/model"]
provider = "openrouter"
model = "manual/model"
max_context_size = 200000
capabilities = ["tool_use"]
`
	if err := os.WriteFile(installation.ConfigPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	document, _ := Load(installation.ConfigPath)
	manual := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(modelInput("manual/model", "openrouter-kimi-code", "openrouter", 200000))})
	if len(manual.Conflicts) != 1 || manual.Conflicts[0].ID != "manual/model" {
		t.Fatalf("manual alias plan = %#v", manual)
	}

	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(installation.ConfigPath)
	ownedBlock := "[models.\"stealth/owned\"]\nprovider = \"openrouter\"\nmodel = \"stealth/owned\"\nmax_context_size = 200000"
	changed := strings.Replace(string(contents), ownedBlock, strings.Replace(ownedBlock, "200000", "210000", 1), 1)
	if err := os.WriteFile(installation.ConfigPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	conflicted := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 220000))})
	if len(conflicted.Conflicts) == 0 || !strings.Contains(conflicted.Conflicts[0].Reason, "max_context_size") {
		t.Fatalf("relevant edit plan = %#v", conflicted)
	}
}

func TestValidZeroRemovesOwnedAliasUnlessItIsProtected(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	provider, _ := ProviderForSource("openrouter")
	document, err := Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "secret"}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(installation.ConfigPath)
	protectedContents := append([]byte("[secondary_model]\nmodel = \"stealth/owned\"\n\n"), contents...)
	if err := os.WriteFile(installation.ConfigPath, protectedContents, 0o600); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	zero := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true}})
	if len(zero.Protected) != 1 || zero.Protected[0].References[0] != "secondary_model.model" {
		t.Fatalf("protected zero plan = %#v", zero)
	}
	if _, remains := zero.Ownership.Models["stealth/owned"]; !remains {
		t.Fatal("protected ownership was discarded")
	}

	unprotectedContents := strings.TrimPrefix(string(protectedContents), "[secondary_model]\nmodel = \"stealth/owned\"\n\n")
	if err := os.WriteFile(installation.ConfigPath, []byte(unprotectedContents), 0o600); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	removed := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true}})
	if len(removed.Conflicts) != 0 || !removed.Write {
		t.Fatalf("removal plan = %#v", removed)
	}
	if err := removed.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(installation.ConfigPath)
	if strings.Contains(string(after), `[models."stealth/owned"]`) {
		t.Fatal("owned alias remains after valid zero")
	}
}

func TestUninstallRemovesOnlyOwnedEntries(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	provider, _ := ProviderForSource("openrouter")
	initial := `[providers.user]
type = "openai"
base_url = "https://example.invalid/v1"
api_key = "secret"
`
	if err := os.WriteFile(installation.ConfigPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	document, _ := Load(installation.ConfigPath)
	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "secret"}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	uninstall := PlanUninstall(document, created.Ownership)
	if len(uninstall.Conflicts) != 0 || !uninstall.Write {
		t.Fatalf("uninstall plan = %#v", uninstall)
	}
	if err := uninstall.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(installation.ConfigPath)
	if !strings.Contains(string(after), "[providers.user]") || strings.Contains(string(after), "[providers.openrouter]") || strings.Contains(string(after), `[models."stealth/owned"]`) {
		t.Fatal("uninstall removed user content or retained owned content")
	}
}

func TestFailedJobPreservesItsManagedAliasWhileSuccessfulZeroRemovesOnlyItsOwn(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	document, err := Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	openRouter, _ := ProviderForSource("openrouter")
	zenMux, _ := ProviderForSource("zenmux")
	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: openRouter, APIKey: "openrouter-secret"}, {Spec: zenMux, APIKey: "zenmux-secret"}}, []JobSelection{
		successfulSelection(modelInput("stealth/openrouter", "openrouter-kimi-code", "openrouter", 200000)),
		{Name: "zenmux-kimi-code", Source: "zenmux", Successful: true, Models: []ModelInput{modelInput("stealth/zenmux", "zenmux-kimi-code", "zenmux", 200000)}},
	})
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	partial := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: openRouter}}, []JobSelection{
		{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true},
		{Name: "zenmux-kimi-code", Source: "zenmux", Successful: false},
	})
	if len(partial.Conflicts) != 0 {
		t.Fatalf("partial plan conflicts = %#v", partial.Conflicts)
	}
	if _, exists := partial.Ownership.Models["stealth/openrouter"]; exists {
		t.Fatal("successful zero did not remove its previous alias")
	}
	if _, exists := partial.Ownership.Models["stealth/zenmux"]; !exists {
		t.Fatal("failed job did not preserve its previous alias")
	}
	if err := partial.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(installation.ConfigPath)
	if strings.Contains(string(after), `[models."stealth/openrouter"]`) || !strings.Contains(string(after), `[models."stealth/zenmux"]`) {
		t.Fatal("partial reconciliation did not isolate the failed job")
	}
}

func TestWholeEntryRemovalRejectsAUserField(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	document, _ := Load(installation.ConfigPath)
	provider, _ := ProviderForSource("openrouter")
	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "secret"}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(installation.ConfigPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("user_note = \"keep\"\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	removal := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true}})
	if len(removal.Conflicts) != 1 || !strings.Contains(removal.Conflicts[0].Reason, "user-owned field") {
		t.Fatalf("removal conflicts = %#v", removal.Conflicts)
	}
}

func TestRepeatedRemovalConvergesWhenStateWriteWasInterrupted(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	document, _ := Load(installation.ConfigPath)
	provider, _ := ProviderForSource("openrouter")
	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "secret"}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	removed := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true}})
	if err := removed.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	retried := PlanSync(document, created.Ownership, []ProviderRequirement{{Spec: provider}}, []JobSelection{{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true}})
	if len(retried.Conflicts) != 0 {
		t.Fatalf("retry conflicts = %#v", retried.Conflicts)
	}
	if _, exists := retried.Ownership.Models["stealth/owned"]; exists {
		t.Fatal("retry did not converge ownership after an already-applied removal")
	}
}

func TestUninstallPreservesAnAdoptedProvider(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	initial := `[providers.openrouter]
type = "openai"
base_url = "https://openrouter.ai/api/v1"
api_key = "user-secret"
custom_headers = { X-Title = "keep" }
`
	if err := os.WriteFile(installation.ConfigPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	document, _ := Load(installation.ConfigPath)
	provider, _ := ProviderForSource("openrouter")
	created := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if len(created.Ownership.Providers) != 0 {
		t.Fatal("adopted provider was claimed")
	}
	if err := created.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	document, _ = Load(installation.ConfigPath)
	uninstall := PlanUninstall(document, created.Ownership)
	if err := uninstall.Apply(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(installation.ConfigPath)
	if !strings.Contains(string(after), "[providers.openrouter]") || !strings.Contains(string(after), `custom_headers = { X-Title = "keep" }`) {
		t.Fatal("adopted provider was not preserved")
	}
}

func TestCompatibleProviderCredentialCanComeFromItsKimiEnvironmentTable(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	initial := `[providers.openrouter]
type = "openai"
base_url = "https://openrouter.ai/api/v1/"

[providers.openrouter.env]
OPENAI_API_KEY = "user-secret"
`
	if err := os.WriteFile(installation.ConfigPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := ProviderForSource("openrouter")
	inspection := document.Provider(provider)
	if !inspection.Compatible || !inspection.CredentialExists {
		t.Fatalf("compatible=%t credential_exists=%t reason=%s", inspection.Compatible, inspection.CredentialExists, inspection.Reason)
	}
	plan := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if len(plan.Conflicts) != 0 {
		t.Fatalf("conflicts = %#v", plan.Conflicts)
	}
	if err := plan.Validate(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.candidate.contents), `[providers.openrouter.env]`) {
		t.Fatal("provider environment table was not preserved")
	}
}

func TestApplyDetectsConcurrentTargetChange(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	document, err := Load(installation.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := ProviderForSource("openrouter")
	plan := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "secret"}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := os.WriteFile(installation.ConfigPath, []byte("# concurrent edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background(), installation); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("apply error = %v", err)
	}
}

func TestIncompatibleProviderIsAConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[providers.openrouter]\ntype = \"anthropic\"\nbase_url = \"https://openrouter.ai/api/v1\"\napi_key = \"secret\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := ProviderForSource("openrouter")
	plan := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider}}, nil)
	if len(plan.Conflicts) != 1 || !strings.Contains(plan.Conflicts[0].Reason, "type") {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestReconciliationKeepsCRLFLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "[providers.openrouter]\r\ntype = \"openai\"\r\nbase_url = \"https://openrouter.ai/api/v1\"\r\napi_key = \"secret\"\r\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := ProviderForSource("openrouter")
	plan := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	withoutCRLF := strings.ReplaceAll(string(plan.candidate.contents), "\r\n", "")
	if strings.Contains(withoutCRLF, "\n") {
		t.Fatal("candidate contains mixed line endings")
	}
}

func TestKnownModelReferencesAreReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := `default_model = "stealth/primary"

[secondary_model]
model = "stealth/secondary"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if references := document.KnownReferences("stealth/primary"); len(references) != 1 || references[0] != "default_model" {
		t.Fatalf("primary references = %#v", references)
	}
	if references := document.KnownReferences("stealth/secondary"); len(references) != 1 || references[0] != "secondary_model.model" {
		t.Fatalf("secondary references = %#v", references)
	}
}

func TestValidationDoesNotWriteTheTarget(t *testing.T) {
	home := t.TempDir()
	installation := realKimi(t, home)
	document, _ := Load(installation.ConfigPath)
	provider, _ := ProviderForSource("openrouter")
	plan := PlanSync(document, state.TargetState{}, []ProviderRequirement{{Spec: provider, APIKey: "secret"}}, []JobSelection{successfulSelection(modelInput("stealth/owned", "openrouter-kimi-code", "openrouter", 200000))})
	if err := plan.Validate(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installation.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("target was written during validation: %v", err)
	}
}

func realKimi(t *testing.T, home string) Installation {
	t.Helper()
	binary, err := exec.LookPath("kimi")
	if err != nil {
		t.Skip("real Kimi Code binary is not installed")
	}
	installation, err := InspectInstallation(context.Background(), binary, home)
	if err != nil {
		t.Skipf("supported Kimi Code binary is unavailable: %v", err)
	}
	return installation
}

func modelInput(id, job, source string, contextWindow int64) ModelInput {
	return ModelInput{ID: id, DisplayName: "Temporary Model", Source: source, Provider: source, Job: job, ContextWindow: contextWindow, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Capabilities: map[string]bool{"reasoning": true}}
}

func successfulSelection(models ...ModelInput) JobSelection {
	return JobSelection{Name: "openrouter-kimi-code", Source: "openrouter", Successful: true, Models: models}
}
