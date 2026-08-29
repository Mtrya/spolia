package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mtrya/spolia/internal/app"
)

func TestJSONCarriesVersionedMachineDecisions(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "sync",
		DryRun:        true,
		Outcome:       "success",
		Jobs:          []app.JobResult{},
		TargetPlans:   []app.TargetPlan{},
	}
	var buffer bytes.Buffer
	if err := JSON(&buffer, input); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		SchemaVersion int    `json:"schema_version"`
		Operation     string `json:"operation"`
		DryRun        bool   `json:"dry_run"`
		Outcome       string `json:"outcome"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 1 || decoded.Operation != "sync" || !decoded.DryRun || decoded.Outcome != "success" {
		t.Fatalf("decoded result = %#v", decoded)
	}
}

func TestHumanSummaryNamesUsableModelsAndTheKimiCommand(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "setup",
		Outcome:       "success",
		Jobs: []app.JobResult{{
			Name:    "openrouter-kimi-code",
			Source:  "openrouter",
			Target:  "kimi-code",
			Outcome: "selected",
			Selected: []app.SelectedModel{
				{ID: "stealth/example", DisplayName: "Example", Class: "stealth"},
			},
			ExclusionSummary: map[string]int{},
		}},
		TargetPlans: []app.TargetPlan{{Target: "kimi-code", Write: true}},
	}
	var buffer bytes.Buffer
	if err := Human(&buffer, input); err != nil {
		t.Fatal(err)
	}
	text := buffer.String()
	if !strings.Contains(text, "stealth/example") || !strings.Contains(text, "kimi --model 'stealth/example'") {
		t.Fatalf("summary does not tell the user how to use a model:\n%s", text)
	}
}

func TestHumanSummaryExplainsZeroCandidatesWithAnActionableHint(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "setup",
		Outcome:       "success",
		Jobs: []app.JobResult{{
			Name:             "openrouter-kimi-code",
			Source:           "openrouter",
			Target:           "kimi-code",
			Outcome:          "zero_candidates",
			Selected:         []app.SelectedModel{},
			ExclusionSummary: map[string]int{"class_disabled": 16, "paid_model": 261},
		}},
		TargetPlans: []app.TargetPlan{{Target: "kimi-code", Write: false}},
	}
	var buffer bytes.Buffer
	if err := Human(&buffer, input); err != nil {
		t.Fatal(err)
	}
	text := buffer.String()
	if !strings.Contains(text, "16 ordinary free models are available") {
		t.Fatalf("the include_free remedy is missing:\n%s", text)
	}
	if !strings.Contains(text, "Kimi Code configuration unchanged") {
		t.Fatalf("the unchanged target is not acknowledged:\n%s", text)
	}
	if strings.Contains(text, "class_disabled") {
		t.Fatalf("internal exclusion code leaked into human output:\n%s", text)
	}
}

func TestHumanHintTranslatesAuthenticationFailure(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "sync",
		Outcome:       "failure",
		Jobs: []app.JobResult{{
			Name:             "openrouter-kimi-code",
			Source:           "openrouter",
			Target:           "kimi-code",
			Outcome:          "error",
			CredentialEnv:    "OPENROUTER_API_KEY",
			Selected:         []app.SelectedModel{},
			ExclusionSummary: map[string]int{},
			Error:            "catalog request returned HTTP 401",
		}},
		TargetPlans: []app.TargetPlan{},
	}
	var buffer bytes.Buffer
	if err := Human(&buffer, input); err != nil {
		t.Fatal(err)
	}
	text := buffer.String()
	if !strings.Contains(text, "HTTP 401") || !strings.Contains(text, "OPENROUTER_API_KEY") || !strings.Contains(text, "rejected") {
		t.Fatalf("authentication failure was not translated into a remedy:\n%s", text)
	}
}

func TestDryRunSummaryDoesNotAdvertiseUnwrittenModels(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "sync",
		DryRun:        true,
		Outcome:       "success",
		Jobs: []app.JobResult{{
			Name:             "openrouter-kimi-code",
			Source:           "openrouter",
			Target:           "kimi-code",
			Outcome:          "selected",
			Selected:         []app.SelectedModel{{ID: "stealth/example", DisplayName: "Example", Class: "stealth"}},
			ExclusionSummary: map[string]int{},
		}},
		TargetPlans: []app.TargetPlan{{Target: "kimi-code", Write: false}},
	}
	var buffer bytes.Buffer
	if err := Human(&buffer, input); err != nil {
		t.Fatal(err)
	}
	text := buffer.String()
	if strings.Contains(text, "Try one now") {
		t.Fatalf("dry run advertised a command for unwritten models:\n%s", text)
	}
	if !strings.Contains(text, "no changes were written") || !strings.Contains(text, "stealth/example") {
		t.Fatalf("dry run summary is unclear:\n%s", text)
	}
}

func TestSchedulerWarningWithoutSelectionsDoesNotClaimReadiness(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "setup",
		Outcome:       "success",
		Jobs: []app.JobResult{{
			Name:             "openrouter-kimi-code",
			Source:           "openrouter",
			Target:           "kimi-code",
			Outcome:          "zero_candidates",
			Selected:         []app.SelectedModel{},
			ExclusionSummary: map[string]int{},
		}},
		TargetPlans: []app.TargetPlan{{Target: "kimi-code", Write: false}},
		Schedule:    &app.SchedulePlan{Enabled: true, Status: "error", Error: "no systemd user session"},
	}
	var buffer bytes.Buffer
	if err := Human(&buffer, input); err != nil {
		t.Fatal(err)
	}
	text := buffer.String()
	if strings.Contains(text, "Models are ready") {
		t.Fatalf("scheduler warning claimed readiness with zero selections:\n%s", text)
	}
	if !strings.Contains(text, "no matching models") || !strings.Contains(text, "Daily scheduling failed") {
		t.Fatalf("summary is unclear:\n%s", text)
	}
}
