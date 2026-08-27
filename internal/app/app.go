package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/model"
	"github.com/Mtrya/llmloot/internal/policy"
	"github.com/Mtrya/llmloot/internal/source"
	"github.com/Mtrya/llmloot/internal/state"
)

const ResultSchemaVersion = 1

type CredentialResolver interface {
	Resolve(context.Context, string, string) (string, error)
}

type Application struct {
	Adapters    map[string]source.Adapter
	Credentials CredentialResolver
	Now         func() time.Time
}

type catalogResult struct {
	catalog source.Catalog
	err     error
}

type SyncResult struct {
	SchemaVersion int                 `json:"schema_version"`
	Operation     string              `json:"operation"`
	DryRun        bool                `json:"dry_run"`
	Outcome       string              `json:"outcome"`
	Jobs          []JobResult         `json:"jobs"`
	Collisions    []Collision         `json:"collisions,omitempty"`
	TargetPlans   []TargetPlan        `json:"target_plans"`
	Observed      map[string][]string `json:"-"`
	AttemptedAt   time.Time           `json:"-"`
}

type JobResult struct {
	Name               string          `json:"name"`
	Source             string          `json:"source"`
	Target             string          `json:"target"`
	Outcome            string          `json:"outcome"`
	PreservePrevious   bool            `json:"preserve_previous,omitempty"`
	Selected           []SelectedModel `json:"selected"`
	ExclusionSummary   map[string]int  `json:"exclusion_summary"`
	Error              string          `json:"error,omitempty"`
	eligibleCandidates []policy.Decision
	limit              int
}

type SelectedModel struct {
	ID               string                 `json:"id"`
	DisplayName      string                 `json:"display_name"`
	Source           string                 `json:"source"`
	Class            model.EligibilityClass `json:"class"`
	ContextWindow    int64                  `json:"context_window"`
	MaxOutput        *int64                 `json:"max_output,omitempty"`
	InputModalities  []string               `json:"input_modalities,omitempty"`
	OutputModalities []string               `json:"output_modalities,omitempty"`
	Capabilities     map[string]bool        `json:"capabilities,omitempty"`
	Prices           []model.Price          `json:"prices"`
	Evidence         []model.Evidence       `json:"evidence,omitempty"`
}

type Collision struct {
	Target        string `json:"target"`
	ModelID       string `json:"model_id"`
	KeptSource    string `json:"kept_source"`
	SkippedSource string `json:"skipped_source"`
}

type TargetPlan struct {
	Target    string           `json:"target"`
	Adapter   string           `json:"adapter"`
	Path      string           `json:"path,omitempty"`
	Write     bool             `json:"write"`
	Models    []TargetModel    `json:"models"`
	Changes   []TargetChange   `json:"changes,omitempty"`
	Conflicts []TargetConflict `json:"conflicts,omitempty"`
	Protected []ProtectedModel `json:"protected,omitempty"`
}

type TargetChange struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
}

type TargetConflict struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type ProtectedModel struct {
	ID         string   `json:"id"`
	References []string `json:"references"`
}

type TargetModel struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Job    string `json:"job"`
}

func (application Application) DryRun(ctx context.Context, configuration config.Config, currentState state.State, requestedJob string) SyncResult {
	return application.Plan(ctx, configuration, currentState, requestedJob, true)
}

func (application Application) Plan(ctx context.Context, configuration config.Config, currentState state.State, requestedJob string, dryRun bool) SyncResult {
	result := SyncResult{SchemaVersion: ResultSchemaVersion, Operation: "sync", DryRun: dryRun, Observed: make(map[string][]string)}
	jobs, err := configuration.EnabledJobs(requestedJob)
	if err != nil {
		result.Outcome = "failure"
		result.Jobs = []JobResult{{Name: requestedJob, Outcome: "error", PreservePrevious: true, Selected: []SelectedModel{}, ExclusionSummary: map[string]int{}, Error: err.Error()}}
		result.TargetPlans = []TargetPlan{}
		return result
	}

	catalogs := make(map[string]catalogResult)
	for _, job := range jobs {
		if _, fetched := catalogs[job.Source]; fetched {
			continue
		}
		sourceConfig := configuration.Sources[job.Source]
		adapter, exists := application.Adapters[sourceConfig.Adapter]
		if !exists {
			catalogs[job.Source] = catalogResult{err: fmt.Errorf("source %q uses unsupported adapter %q", job.Source, sourceConfig.Adapter)}
			continue
		}
		credential, err := application.Credentials.Resolve(ctx, job.Source, sourceConfig.CredentialEnv)
		if err != nil {
			catalogs[job.Source] = catalogResult{err: err}
			continue
		}
		catalog, err := adapter.Fetch(ctx, credential)
		catalogs[job.Source] = catalogResult{catalog: catalog, err: err}
		if err == nil {
			for _, candidate := range catalog.Models {
				result.Observed[job.Source] = append(result.Observed[job.Source], candidate.ID)
			}
			sort.Strings(result.Observed[job.Source])
		}
	}

	now := time.Now().UTC()
	if application.Now != nil {
		now = application.Now().UTC()
	}
	planned := plan(configuration, currentState, jobs, catalogs, now, requestedJob)
	planned.DryRun = dryRun
	planned.Observed = result.Observed
	planned.AttemptedAt = now
	return planned
}

func plan(configuration config.Config, currentState state.State, jobs []config.NamedJob, catalogs map[string]catalogResult, now time.Time, requestedJob ...string) SyncResult {
	result := SyncResult{SchemaVersion: ResultSchemaVersion, Operation: "sync", DryRun: true}
	for _, job := range jobs {
		jobResult := JobResult{
			Name:             job.Name,
			Source:           job.Source,
			Target:           job.Target,
			Selected:         []SelectedModel{},
			ExclusionSummary: make(map[string]int),
			limit:            job.Limit,
		}
		fetched := catalogs[job.Source]
		if fetched.err != nil {
			jobResult.Outcome = "error"
			jobResult.PreservePrevious = true
			jobResult.Error = fetched.err.Error()
			result.Jobs = append(result.Jobs, jobResult)
			continue
		}
		for code, count := range fetched.catalog.InvalidRecords {
			jobResult.ExclusionSummary[code] += count
		}
		candidates := append([]model.Candidate(nil), fetched.catalog.Models...)
		for index := range candidates {
			candidates[index].FirstSeen = currentState.FirstSeen(job.Source, candidates[index].ID)
			if candidates[index].FirstSeen.IsZero() {
				candidates[index].FirstSeen = now
			}
		}
		decisions := policy.Evaluate(candidates, policy.Options{Policy: job.Policy, MinContext: job.MinContext, Now: now})
		for _, decision := range decisions {
			if decision.Eligible {
				jobResult.eligibleCandidates = append(jobResult.eligibleCandidates, decision)
				continue
			}
			for _, reason := range decision.Reasons {
				jobResult.ExclusionSummary[reason.Code]++
			}
		}
		result.Jobs = append(result.Jobs, jobResult)
	}

	used := make(map[string]map[string]TargetModel)
	if len(requestedJob) > 0 && requestedJob[0] != "" && len(jobs) == 1 {
		priority := make(map[string]int, len(configuration.SourcePriority))
		for index, sourceName := range configuration.SourcePriority {
			priority[sourceName] = index
		}
		requested := jobs[0]
		for alias, owned := range currentState.Targets[requested.Target].Models {
			if owned.Job == requested.Name || priority[owned.Source] > priority[requested.Source] {
				continue
			}
			if used[requested.Target] == nil {
				used[requested.Target] = make(map[string]TargetModel)
			}
			used[requested.Target][alias] = TargetModel{ID: alias, Source: owned.Source, Job: owned.Job}
		}
	}
	for index := range result.Jobs {
		jobResult := &result.Jobs[index]
		if jobResult.Outcome == "error" {
			continue
		}
		if used[jobResult.Target] == nil {
			used[jobResult.Target] = make(map[string]TargetModel)
		}
		for _, decision := range jobResult.eligibleCandidates {
			if len(jobResult.Selected) >= jobResult.limit {
				break
			}
			if winner, exists := used[jobResult.Target][decision.Candidate.ID]; exists {
				jobResult.ExclusionSummary["source_collision"]++
				result.Collisions = append(result.Collisions, Collision{Target: jobResult.Target, ModelID: decision.Candidate.ID, KeptSource: winner.Source, SkippedSource: jobResult.Source})
				continue
			}
			selected := selectedModel(decision)
			jobResult.Selected = append(jobResult.Selected, selected)
			used[jobResult.Target][selected.ID] = TargetModel{ID: selected.ID, Source: selected.Source, Job: jobResult.Name}
		}
		if len(jobResult.Selected) == 0 {
			jobResult.Outcome = "zero_candidates"
		} else {
			jobResult.Outcome = "selected"
		}
		jobResult.eligibleCandidates = nil
		jobResult.limit = 0
	}

	targetNames := make([]string, 0, len(used))
	for targetName := range used {
		targetNames = append(targetNames, targetName)
	}
	sort.Strings(targetNames)
	for _, targetName := range targetNames {
		models := make([]TargetModel, 0, len(used[targetName]))
		for _, targetModel := range used[targetName] {
			models = append(models, targetModel)
		}
		sort.Slice(models, func(left, right int) bool { return models[left].ID < models[right].ID })
		result.TargetPlans = append(result.TargetPlans, TargetPlan{Target: targetName, Adapter: configuration.Targets[targetName].Adapter, Write: false, Models: models})
	}
	if result.TargetPlans == nil {
		result.TargetPlans = []TargetPlan{}
	}
	result.Outcome = overallOutcome(result.Jobs)
	return result
}

func selectedModel(decision policy.Decision) SelectedModel {
	candidate := decision.Candidate
	return SelectedModel{
		ID:               candidate.ID,
		DisplayName:      candidate.DisplayName,
		Source:           candidate.Source,
		Class:            decision.Class,
		ContextWindow:    candidate.ContextWindow,
		MaxOutput:        candidate.MaxOutput,
		InputModalities:  candidate.InputModalities,
		OutputModalities: candidate.OutputModalities,
		Capabilities:     candidate.Capabilities,
		Prices:           candidate.Prices,
		Evidence:         candidate.Evidence,
	}
}

func overallOutcome(jobs []JobResult) string {
	failures := 0
	for _, job := range jobs {
		if job.Outcome == "error" {
			failures++
		}
	}
	if failures == 0 {
		return "success"
	}
	if failures == len(jobs) {
		return "failure"
	}
	return "partial_failure"
}
