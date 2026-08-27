package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/model"
	"github.com/Mtrya/llmloot/internal/source"
	"github.com/Mtrya/llmloot/internal/state"
)

func TestPlanResolvesCollisionsBySourcePriorityAndFillsTheLowerPriorityLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	configuration := twoSourceConfiguration()
	jobs, err := configuration.EnabledJobs("")
	if err != nil {
		t.Fatal(err)
	}
	sharedOpenRouter := selectableCandidate("openrouter", "shared/model", now)
	openRouterOnly := selectableCandidate("openrouter", "openrouter/only", now.Add(-time.Hour))
	sharedZenMux := selectableCandidate("zenmux", "shared/model", now)
	zenMuxOnly := selectableCandidate("zenmux", "zenmux/only", now.Add(-time.Hour))

	result := plan(configuration, state.State{SchemaVersion: state.SchemaVersion}, jobs, map[string]catalogResult{
		"openrouter": {catalog: source.Catalog{Source: "openrouter", Models: []model.Candidate{sharedOpenRouter, openRouterOnly}}},
		"zenmux":     {catalog: source.Catalog{Source: "zenmux", Models: []model.Candidate{sharedZenMux, zenMuxOnly}}},
	}, now)

	if result.Outcome != "success" || len(result.Collisions) != 1 {
		t.Fatalf("result outcome/collisions = %q/%#v", result.Outcome, result.Collisions)
	}
	if result.Collisions[0].KeptSource != "openrouter" || result.Collisions[0].SkippedSource != "zenmux" {
		t.Fatalf("collision = %#v", result.Collisions[0])
	}
	if len(result.Jobs[0].Selected) != 1 || result.Jobs[0].Selected[0].ID != "shared/model" {
		t.Fatalf("OpenRouter selection = %#v", result.Jobs[0].Selected)
	}
	if len(result.Jobs[1].Selected) != 1 || result.Jobs[1].Selected[0].ID != "zenmux/only" {
		t.Fatalf("ZenMux selection = %#v", result.Jobs[1].Selected)
	}
	if len(result.TargetPlans) != 1 || result.TargetPlans[0].Write || len(result.TargetPlans[0].Models) != 2 {
		t.Fatalf("target plans = %#v", result.TargetPlans)
	}
}

func TestPlanIsolatesSourceFailureFromSuccessfulValidZero(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	configuration := twoSourceConfiguration()
	jobs, err := configuration.EnabledJobs("")
	if err != nil {
		t.Fatal(err)
	}
	result := plan(configuration, state.State{SchemaVersion: state.SchemaVersion}, jobs, map[string]catalogResult{
		"openrouter": {err: errors.New("catalog unavailable")},
		"zenmux":     {catalog: source.Catalog{Source: "zenmux", Models: []model.Candidate{}}},
	}, now)

	if result.Outcome != "partial_failure" {
		t.Fatalf("outcome = %q", result.Outcome)
	}
	if result.Jobs[0].Outcome != "error" || !result.Jobs[0].PreservePrevious {
		t.Fatalf("failed job = %#v", result.Jobs[0])
	}
	if result.Jobs[1].Outcome != "zero_candidates" || result.Jobs[1].PreservePrevious {
		t.Fatalf("zero job = %#v", result.Jobs[1])
	}
}

func TestRequestedLowerPriorityJobDoesNotDisplaceOwnedHigherPriorityAlias(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	configuration := twoSourceConfiguration()
	jobs, err := configuration.EnabledJobs("zenmux-kimi-code")
	if err != nil {
		t.Fatal(err)
	}
	currentState := state.New()
	currentState.Targets["kimi-code"] = state.TargetState{Models: map[string]state.ModelOwnership{
		"shared/model": {Source: "openrouter", Job: "openrouter-kimi-code"},
	}}
	result := plan(configuration, currentState, jobs, map[string]catalogResult{
		"zenmux": {catalog: source.Catalog{Source: "zenmux", Models: []model.Candidate{selectableCandidate("zenmux", "shared/model", now)}}},
	}, now, "zenmux-kimi-code")
	if len(result.Collisions) != 1 || result.Collisions[0].KeptSource != "openrouter" {
		t.Fatalf("collisions = %#v", result.Collisions)
	}
	if len(result.Jobs[0].Selected) != 0 || result.Jobs[0].Outcome != "zero_candidates" {
		t.Fatalf("requested job = %#v", result.Jobs[0])
	}
}

func twoSourceConfiguration() config.Config {
	return config.Config{
		SchemaVersion:  1,
		SourcePriority: []string{"openrouter", "zenmux"},
		Sources: map[string]config.Source{
			"openrouter": {Adapter: "openrouter", CredentialEnv: "OPENROUTER_API_KEY"},
			"zenmux":     {Adapter: "zenmux", CredentialEnv: "ZENMUX_API_KEY"},
		},
		Targets: map[string]config.Target{"kimi-code": {Adapter: "kimi-code"}},
		Jobs: map[string]config.Job{
			"openrouter-kimi-code": {Enabled: true, Source: "openrouter", Target: "kimi-code", Limit: 1, MinContext: 131072},
			"zenmux-kimi-code":     {Enabled: true, Source: "zenmux", Target: "kimi-code", Limit: 1, MinContext: 131072},
		},
	}
}

func selectableCandidate(sourceName, id string, created time.Time) model.Candidate {
	return model.Candidate{
		Source:           sourceName,
		ID:               id,
		DisplayName:      id,
		Kind:             model.KindModel,
		Protocol:         "openai_chat",
		CreatedAt:        created,
		ContextWindow:    200000,
		OutputModalities: []string{"text"},
		Tools:            model.Bool(true),
		Prices: []model.Price{
			{Dimension: "prompt", Value: "0", Unit: "per_token", Currency: "USD"},
			{Dimension: "completion", Value: "0", Unit: "per_token", Currency: "USD"},
		},
		Evidence: []model.Evidence{{Class: model.ClassStealth, Kind: "official_namespace", Field: "id"}},
	}
}
