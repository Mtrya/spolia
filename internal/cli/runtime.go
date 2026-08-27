package cli

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/source"
	"github.com/Mtrya/llmloot/internal/source/openrouter"
	"github.com/Mtrya/llmloot/internal/source/zenmux"
	"github.com/Mtrya/llmloot/internal/state"
	"github.com/Mtrya/llmloot/internal/target/kimicode"
)

func application(credentials app.CredentialResolver) app.Application {
	client := &http.Client{Timeout: 30 * time.Second}
	return app.Application{
		Adapters: map[string]source.Adapter{
			"openrouter": openrouter.New(client),
			"zenmux":     zenmux.New(client),
		},
		Credentials: credentials,
	}
}

func providersForJobs(configuration config.Config, jobs []config.NamedJob) (map[string]kimicode.ProviderSpec, error) {
	providers := make(map[string]kimicode.ProviderSpec)
	for _, job := range jobs {
		sourceConfiguration := configuration.Sources[job.Source]
		provider, err := kimicode.ProviderForSource(sourceConfiguration.Adapter)
		if err != nil {
			return nil, err
		}
		providers[job.Source] = provider
	}
	return providers, nil
}

func providerRequirements(ctx context.Context, credentials kimicode.Credentials, providers map[string]kimicode.ProviderSpec) []kimicode.ProviderRequirement {
	sourceNames := make([]string, 0, len(providers))
	for sourceName := range providers {
		sourceNames = append(sourceNames, sourceName)
	}
	sort.Strings(sourceNames)
	var requirements []kimicode.ProviderRequirement
	for _, sourceName := range sourceNames {
		credential, err := credentials.Resolve(ctx, sourceName, "")
		if err != nil {
			continue
		}
		requirements = append(requirements, kimicode.ProviderRequirement{Spec: providers[sourceName], APIKey: credential})
	}
	return requirements
}

func selections(result app.SyncResult, providers map[string]kimicode.ProviderSpec) []kimicode.JobSelection {
	selections := make([]kimicode.JobSelection, 0, len(result.Jobs))
	for _, job := range result.Jobs {
		selection := kimicode.JobSelection{Name: job.Name, Source: job.Source, Successful: job.Outcome != "error"}
		provider := providers[job.Source]
		for _, selected := range job.Selected {
			selection.Models = append(selection.Models, kimicode.ModelInput{
				ID:               selected.ID,
				DisplayName:      selected.DisplayName,
				Source:           job.Source,
				Provider:         provider.ID,
				Job:              job.Name,
				ContextWindow:    selected.ContextWindow,
				MaxOutput:        selected.MaxOutput,
				InputModalities:  append([]string(nil), selected.InputModalities...),
				OutputModalities: append([]string(nil), selected.OutputModalities...),
				Capabilities:     cloneCapabilities(selected.Capabilities),
			})
		}
		selections = append(selections, selection)
	}
	return selections
}

func cloneCapabilities(input map[string]bool) map[string]bool {
	result := make(map[string]bool, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func attachTargetPlan(result *app.SyncResult, targetName, adapter string, plan kimicode.Plan) {
	models := make([]app.TargetModel, 0)
	for _, job := range result.Jobs {
		if job.Target != targetName {
			continue
		}
		for _, selected := range job.Selected {
			models = append(models, app.TargetModel{ID: selected.ID, Source: job.Source, Job: job.Name})
		}
	}
	sort.Slice(models, func(left, right int) bool { return models[left].ID < models[right].ID })
	targetPlan := app.TargetPlan{Target: targetName, Adapter: adapter, Path: plan.Path, Write: plan.Write, Models: models}
	for _, change := range plan.Changes {
		targetPlan.Changes = append(targetPlan.Changes, app.TargetChange{Action: change.Action, Kind: change.Kind, ID: change.ID})
	}
	for _, conflict := range plan.Conflicts {
		targetPlan.Conflicts = append(targetPlan.Conflicts, app.TargetConflict{Kind: conflict.Kind, ID: conflict.ID, Reason: conflict.Reason})
	}
	for _, protected := range plan.Protected {
		targetPlan.Protected = append(targetPlan.Protected, app.ProtectedModel{ID: protected.ID, References: protected.References})
	}
	result.TargetPlans = []app.TargetPlan{targetPlan}
	if len(plan.Conflicts) > 0 {
		result.Outcome = "failure"
	}
}

func updateState(current *state.State, result app.SyncResult, targetName string, ownership state.TargetState) {
	current.LLMlootVersion = Version
	for sourceName, modelIDs := range result.Observed {
		current.Observe(sourceName, modelIDs, result.AttemptedAt)
	}
	for _, job := range result.Jobs {
		selectedIDs := make([]string, 0, len(job.Selected))
		for _, selected := range job.Selected {
			selectedIDs = append(selectedIDs, selected.ID)
		}
		if job.Outcome == "error" {
			selectedIDs = append([]string(nil), current.Jobs[job.Name].SelectedIDs...)
		}
		current.RecordJob(job.Name, job.Outcome, selectedIDs, compactError(job.Error), result.AttemptedAt)
	}
	current.Targets[targetName] = ownership
}

func compactError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}

func onlyKimiTarget(configuration config.Config) (string, error) {
	var targetName string
	for name, target := range configuration.Targets {
		if target.Adapter != "kimi-code" {
			return "", fmt.Errorf("target %q uses unsupported adapter %q", name, target.Adapter)
		}
		if targetName != "" {
			return "", fmt.Errorf("this version supports one Kimi Code target")
		}
		targetName = name
	}
	if targetName == "" {
		return "", fmt.Errorf("config has no Kimi Code target")
	}
	return targetName, nil
}
