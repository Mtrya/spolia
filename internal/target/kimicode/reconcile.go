package kimicode

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Mtrya/spolia/internal/atomicfile"
	"github.com/Mtrya/spolia/internal/state"
)

type ProviderRequirement struct {
	Spec   ProviderSpec
	APIKey string `json:"-"`
}

type ModelInput struct {
	ID               string
	DisplayName      string
	Source           string
	Provider         string
	Job              string
	ContextWindow    int64
	MaxOutput        *int64
	InputModalities  []string
	OutputModalities []string
	Capabilities     map[string]bool
}

type JobSelection struct {
	Name       string
	Source     string
	Successful bool
	Models     []ModelInput
}

type Change struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
}

type Conflict struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type ProtectedModel struct {
	ID         string   `json:"id"`
	References []string `json:"references"`
}

type Plan struct {
	Path      string            `json:"path"`
	Write     bool              `json:"write"`
	Changes   []Change          `json:"changes"`
	Conflicts []Conflict        `json:"conflicts"`
	Protected []ProtectedModel  `json:"protected"`
	Ownership state.TargetState `json:"-"`
	original  Document
	candidate Document
}

func InspectOwnership(document Document, ownership state.TargetState) []Conflict {
	var conflicts []Conflict
	if ownership.Path != "" && ownership.Path != document.path {
		conflicts = append(conflicts, Conflict{Kind: "target", ID: document.path, Reason: "ownership state points to a different Kimi Code config path"})
	}
	providerIDs := make([]string, 0, len(ownership.Providers))
	for providerID := range ownership.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		if reason := changedOwnedFields(document, []string{"providers", providerID}, ownership.Providers[providerID].Fields); reason != "" {
			conflicts = append(conflicts, Conflict{Kind: "provider", ID: providerID, Reason: reason})
		}
	}
	modelIDs := sortedOwnershipModels(ownership.Models)
	for _, modelID := range modelIDs {
		if reason := changedOwnedFields(document, []string{"models", modelID}, ownership.Models[modelID].Fields); reason != "" {
			conflicts = append(conflicts, Conflict{Kind: "model", ID: modelID, Reason: reason})
		}
	}
	return conflicts
}

func PlanSync(document Document, ownership state.TargetState, providers []ProviderRequirement, selections []JobSelection) Plan {
	plan := newPlan(document, ownership)
	for _, requirement := range providers {
		plan.ensureProvider(requirement)
	}

	desiredByJob := make(map[string]map[string]ModelInput)
	successfulJobs := make(map[string]bool)
	for _, selection := range selections {
		if !selection.Successful {
			continue
		}
		successfulJobs[selection.Name] = true
		desiredByJob[selection.Name] = make(map[string]ModelInput)
		for _, desired := range selection.Models {
			if previous, duplicate := desiredByJob[selection.Name][desired.ID]; duplicate && previous.Source != desired.Source {
				plan.addConflict("model", desired.ID, "more than one source selected the same alias")
				continue
			}
			desiredByJob[selection.Name][desired.ID] = desired
		}
	}

	aliases := make([]string, 0, len(plan.Ownership.Models))
	for alias := range plan.Ownership.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		owned := plan.Ownership.Models[alias]
		if !successfulJobs[owned.Job] {
			continue
		}
		if _, remains := desiredByJob[owned.Job][alias]; remains {
			continue
		}
		plan.removeManagedModel(alias, owned)
	}

	jobNames := make([]string, 0, len(desiredByJob))
	for jobName := range desiredByJob {
		jobNames = append(jobNames, jobName)
	}
	sort.Strings(jobNames)
	for _, jobName := range jobNames {
		modelIDs := make([]string, 0, len(desiredByJob[jobName]))
		for modelID := range desiredByJob[jobName] {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		for _, modelID := range modelIDs {
			plan.reconcileModel(desiredByJob[jobName][modelID])
		}
	}
	plan.finish()
	return plan
}

func PlanUninstall(document Document, ownership state.TargetState) Plan {
	plan := newPlan(document, ownership)
	modelIDs := sortedOwnershipModels(plan.Ownership.Models)
	for _, modelID := range modelIDs {
		plan.removeManagedModel(modelID, plan.Ownership.Models[modelID])
	}
	providerIDs := make([]string, 0, len(plan.Ownership.Providers))
	for providerID := range plan.Ownership.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		plan.removeManagedProvider(providerID, plan.Ownership.Providers[providerID])
	}
	plan.finish()
	return plan
}

func newPlan(document Document, ownership state.TargetState) Plan {
	pathConflict := ownership.Path != "" && ownership.Path != document.path
	if ownership.Providers == nil {
		ownership.Providers = make(map[string]state.ProviderOwnership)
	}
	if ownership.Models == nil {
		ownership.Models = make(map[string]state.ModelOwnership)
	}
	providers := make(map[string]state.ProviderOwnership, len(ownership.Providers))
	for providerID, owned := range ownership.Providers {
		providers[providerID] = state.ProviderOwnership{Fields: cloneStrings(owned.Fields)}
	}
	models := make(map[string]state.ModelOwnership, len(ownership.Models))
	for modelID, owned := range ownership.Models {
		models[modelID] = state.ModelOwnership{Source: owned.Source, Job: owned.Job, Fields: cloneStrings(owned.Fields)}
	}
	ownership.Path = document.path
	ownership.Providers = providers
	ownership.Models = models
	plan := Plan{Path: document.path, Changes: []Change{}, Conflicts: []Conflict{}, Protected: []ProtectedModel{}, Ownership: ownership, original: document, candidate: document}
	if pathConflict {
		plan.addConflict("target", document.path, "ownership state points to a different Kimi Code config path")
	}
	return plan
}

func (plan *Plan) ensureProvider(requirement ProviderRequirement) {
	inspection := plan.candidate.Provider(requirement.Spec)
	owned, wasOwned := plan.Ownership.Providers[requirement.Spec.ID]
	if !inspection.Exists {
		if wasOwned {
			plan.addConflict("provider", requirement.Spec.ID, "spolia-owned provider is missing")
			return
		}
		if requirement.APIKey == "" {
			plan.addConflict("provider", requirement.Spec.ID, "provider credential is missing")
			return
		}
		fields := providerFields(requirement.Spec)
		updated, err := plan.candidate.appendTable("[providers."+requirement.Spec.ID+"]", []fieldPair{
			{key: "type", value: fields["type"]},
			{key: "base_url", value: fields["base_url"]},
			{key: "api_key", value: renderedField{semantic: stringSemantic(requirement.APIKey), rendered: quote(requirement.APIKey)}},
		})
		if err != nil {
			plan.addConflict("provider", requirement.Spec.ID, "cannot create provider entry")
			return
		}
		plan.candidate = updated
		plan.Ownership.Providers[requirement.Spec.ID] = state.ProviderOwnership{Fields: semanticFields(fields)}
		plan.Changes = append(plan.Changes, Change{Action: "add", Kind: "provider", ID: requirement.Spec.ID})
		return
	}
	if !inspection.Compatible {
		plan.addConflict("provider", requirement.Spec.ID, inspection.Reason)
		return
	}
	if wasOwned {
		if reason := changedOwnedFields(plan.candidate, []string{"providers", requirement.Spec.ID}, owned.Fields); reason != "" {
			plan.addConflict("provider", requirement.Spec.ID, reason)
			return
		}
	}
	if inspection.CredentialExists {
		return
	}
	if requirement.APIKey == "" {
		plan.addConflict("provider", requirement.Spec.ID, "provider credential is missing")
		return
	}
	updated, err := plan.candidate.setField([]string{"providers", requirement.Spec.ID}, "api_key", renderedField{semantic: stringSemantic(requirement.APIKey), rendered: quote(requirement.APIKey)})
	if err != nil {
		plan.addConflict("provider", requirement.Spec.ID, "cannot add provider credential")
		return
	}
	plan.candidate = updated
	plan.Changes = append(plan.Changes, Change{Action: "update", Kind: "provider", ID: requirement.Spec.ID})
}

func providerFields(specification ProviderSpec) map[string]renderedField {
	return map[string]renderedField{
		"type":     {semantic: stringSemantic(specification.Type), rendered: quote(specification.Type)},
		"base_url": {semantic: stringSemantic(normalizedURL(specification.BaseURL)), rendered: quote(specification.BaseURL)},
	}
}

func modelFields(desired ModelInput) map[string]renderedField {
	capabilities := kimiCapabilities(desired)
	fields := map[string]renderedField{
		"provider":         {semantic: stringSemantic(desired.Provider), rendered: quote(desired.Provider)},
		"model":            {semantic: stringSemantic(desired.ID), rendered: quote(desired.ID)},
		"max_context_size": {semantic: intSemantic(desired.ContextWindow), rendered: intSemantic(desired.ContextWindow)},
		"capabilities":     {semantic: stringsSemantic(capabilities), rendered: renderStrings(capabilities)},
		"display_name":     {semantic: stringSemantic(desired.DisplayName), rendered: quote(desired.DisplayName)},
	}
	if desired.MaxOutput != nil {
		fields["max_output_size"] = renderedField{semantic: intSemantic(*desired.MaxOutput), rendered: intSemantic(*desired.MaxOutput)}
	}
	return fields
}

func kimiCapabilities(desired ModelInput) []string {
	capabilities := map[string]bool{"tool_use": true}
	if desired.Capabilities["reasoning"] || desired.Capabilities["thinking"] {
		capabilities["thinking"] = true
	}
	if desired.Capabilities["always_thinking"] {
		capabilities["thinking"] = true
		capabilities["always_thinking"] = true
	}
	for _, modality := range desired.InputModalities {
		switch modality {
		case "image", "video", "audio":
			capabilities[modality+"_in"] = true
		}
	}
	for _, modality := range desired.OutputModalities {
		switch modality {
		case "image", "audio":
			capabilities[modality+"_out"] = true
		}
	}
	result := make([]string, 0, len(capabilities))
	for capability := range capabilities {
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func (plan *Plan) reconcileModel(desired ModelInput) {
	fields := modelFields(desired)
	owned, wasOwned := plan.Ownership.Models[desired.ID]
	entry, exists := plan.candidate.model(desired.ID)
	if !wasOwned {
		if exists {
			plan.addConflict("model", desired.ID, "alias already belongs to the user")
			return
		}
		pairs := orderedModelFields(fields)
		updated, err := plan.candidate.appendTable("[models."+quote(desired.ID)+"]", pairs)
		if err != nil {
			plan.addConflict("model", desired.ID, "cannot create model alias")
			return
		}
		plan.candidate = updated
		plan.Ownership.Models[desired.ID] = state.ModelOwnership{Source: desired.Source, Job: desired.Job, Fields: semanticFields(fields)}
		plan.Changes = append(plan.Changes, Change{Action: "add", Kind: "model", ID: desired.ID})
		return
	}
	if !exists || entry == nil {
		plan.addConflict("model", desired.ID, "spolia-owned alias is missing")
		return
	}
	keys := unionKeys(owned.Fields, fields)
	updated := plan.candidate
	changed := false
	for _, key := range keys {
		last, previouslyOwned := owned.Fields[key]
		desiredValue, wanted := fields[key]
		currentEntry, _ := updated.model(desired.ID)
		currentValue, currentExists := currentEntry[key]
		currentSemantic := ""
		if currentExists {
			currentSemantic, _ = semanticField(key, currentValue)
		}
		if wanted {
			if previouslyOwned && !currentExists {
				plan.addConflict("model", desired.ID, fmt.Sprintf("managed field %q was removed", key))
				return
			}
			if currentExists && currentSemantic != desiredValue.semantic && (!previouslyOwned || currentSemantic != last) {
				plan.addConflict("model", desired.ID, fmt.Sprintf("managed field %q was changed", key))
				return
			}
			if !currentExists || currentSemantic != desiredValue.semantic {
				var err error
				updated, err = updated.setField([]string{"models", desired.ID}, key, desiredValue)
				if err != nil {
					plan.addConflict("model", desired.ID, fmt.Sprintf("cannot update field %q", key))
					return
				}
				changed = true
			}
			continue
		}
		if previouslyOwned && currentExists {
			if currentSemantic != last {
				plan.addConflict("model", desired.ID, fmt.Sprintf("managed field %q was changed", key))
				return
			}
			var err error
			updated, err = updated.removeField([]string{"models", desired.ID}, key)
			if err != nil {
				plan.addConflict("model", desired.ID, fmt.Sprintf("cannot remove field %q", key))
				return
			}
			changed = true
		}
	}
	plan.candidate = updated
	plan.Ownership.Models[desired.ID] = state.ModelOwnership{Source: desired.Source, Job: desired.Job, Fields: semanticFields(fields)}
	if changed || owned.Source != desired.Source || owned.Job != desired.Job {
		plan.Changes = append(plan.Changes, Change{Action: "update", Kind: "model", ID: desired.ID})
	}
}

func (plan *Plan) removeManagedModel(alias string, owned state.ModelOwnership) {
	entry, exists := plan.candidate.model(alias)
	if !exists || entry == nil {
		delete(plan.Ownership.Models, alias)
		return
	}
	if references := plan.candidate.KnownReferences(alias); len(references) > 0 {
		plan.Protected = append(plan.Protected, ProtectedModel{ID: alias, References: references})
		return
	}
	if reason := unsafeWholeEntry(entry, owned.Fields, nil); reason != "" {
		plan.addConflict("model", alias, reason)
		return
	}
	updated, err := plan.candidate.removeEntry([]string{"models", alias})
	if err != nil {
		plan.addConflict("model", alias, "cannot remove model alias")
		return
	}
	plan.candidate = updated
	delete(plan.Ownership.Models, alias)
	plan.Changes = append(plan.Changes, Change{Action: "remove", Kind: "model", ID: alias})
}

func (plan *Plan) removeManagedProvider(providerID string, owned state.ProviderOwnership) {
	providers := table(plan.candidate.root, "providers")
	entry, exists := providers[providerID].(map[string]any)
	if !exists {
		delete(plan.Ownership.Providers, providerID)
		return
	}
	if reason := unsafeWholeEntry(entry, owned.Fields, map[string]bool{"api_key": true}); reason != "" {
		plan.addConflict("provider", providerID, reason)
		return
	}
	for alias, value := range table(plan.candidate.root, "models") {
		modelEntry, okay := value.(map[string]any)
		if !okay {
			continue
		}
		if provider, _ := modelEntry["provider"].(string); provider == providerID {
			plan.addConflict("provider", providerID, fmt.Sprintf("provider is still referenced by model %q", alias))
			return
		}
	}
	updated, err := plan.candidate.removeEntry([]string{"providers", providerID})
	if err != nil {
		plan.addConflict("provider", providerID, "cannot remove provider")
		return
	}
	plan.candidate = updated
	delete(plan.Ownership.Providers, providerID)
	plan.Changes = append(plan.Changes, Change{Action: "remove", Kind: "provider", ID: providerID})
}

func unsafeWholeEntry(entry map[string]any, expected map[string]string, allowedExtra map[string]bool) string {
	for key, expectedValue := range expected {
		current, exists := entry[key]
		if !exists {
			return fmt.Sprintf("managed field %q is missing", key)
		}
		currentValue, err := semanticField(key, current)
		if err != nil || currentValue != expectedValue {
			return fmt.Sprintf("managed field %q was changed", key)
		}
	}
	for key := range entry {
		if _, managed := expected[key]; !managed && !allowedExtra[key] {
			return fmt.Sprintf("entry contains user-owned field or subtable %q", key)
		}
	}
	return ""
}

func changedOwnedFields(document Document, path []string, expected map[string]string) string {
	current := document.root
	for _, part := range path {
		next, okay := current[part].(map[string]any)
		if !okay {
			return "owned entry is missing"
		}
		current = next
	}
	for key, expectedValue := range expected {
		value, exists := current[key]
		if !exists {
			return fmt.Sprintf("managed field %q is missing", key)
		}
		semantic, err := semanticField(key, value)
		if err != nil || semantic != expectedValue {
			return fmt.Sprintf("managed field %q was changed", key)
		}
	}
	return ""
}

func semanticFields(fields map[string]renderedField) map[string]string {
	result := make(map[string]string, len(fields))
	for key, value := range fields {
		result[key] = value.semantic
	}
	return result
}

func orderedModelFields(fields map[string]renderedField) []fieldPair {
	order := []string{"provider", "model", "max_context_size", "max_output_size", "capabilities", "display_name"}
	result := make([]fieldPair, 0, len(fields))
	for _, key := range order {
		if value, exists := fields[key]; exists {
			result = append(result, fieldPair{key: key, value: value})
		}
	}
	return result
}

func unionKeys(previous map[string]string, desired map[string]renderedField) []string {
	set := make(map[string]bool, len(previous)+len(desired))
	for key := range previous {
		set[key] = true
	}
	for key := range desired {
		set[key] = true
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedOwnershipModels(models map[string]state.ModelOwnership) []string {
	result := make([]string, 0, len(models))
	for modelID := range models {
		result = append(result, modelID)
	}
	sort.Strings(result)
	return result
}

func cloneStrings(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (plan *Plan) addConflict(kind, id, reason string) {
	plan.Conflicts = append(plan.Conflicts, Conflict{Kind: kind, ID: id, Reason: reason})
}

func (plan *Plan) finish() {
	plan.Write = !plan.originalEqualCandidate()
	sort.Slice(plan.Changes, func(left, right int) bool {
		if plan.Changes[left].Kind != plan.Changes[right].Kind {
			return plan.Changes[left].Kind < plan.Changes[right].Kind
		}
		if plan.Changes[left].ID != plan.Changes[right].ID {
			return plan.Changes[left].ID < plan.Changes[right].ID
		}
		return plan.Changes[left].Action < plan.Changes[right].Action
	})
	sort.Slice(plan.Conflicts, func(left, right int) bool {
		if plan.Conflicts[left].Kind != plan.Conflicts[right].Kind {
			return plan.Conflicts[left].Kind < plan.Conflicts[right].Kind
		}
		return plan.Conflicts[left].ID < plan.Conflicts[right].ID
	})
	sort.Slice(plan.Protected, func(left, right int) bool { return plan.Protected[left].ID < plan.Protected[right].ID })
}

func (plan Plan) originalEqualCandidate() bool {
	return string(plan.original.contents) == string(plan.candidate.contents)
}

func (plan Plan) Validate(ctx context.Context, installation Installation) error {
	if len(plan.Conflicts) > 0 {
		return errors.New("target plan has conflicts")
	}
	temporary, err := os.CreateTemp("", "spolia-kimi-config-*.toml")
	if err != nil {
		return fmt.Errorf("create validation file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure validation file: %w", err)
	}
	if _, err := temporary.Write(plan.candidate.contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write validation file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close validation file: %w", err)
	}
	return installation.Validate(ctx, temporaryPath)
}

func (plan Plan) Apply(ctx context.Context, installation Installation) error {
	if err := plan.Validate(ctx, installation); err != nil {
		return err
	}
	if !plan.Write {
		return nil
	}
	current, err := os.ReadFile(plan.Path)
	if errors.Is(err, os.ErrNotExist) {
		if plan.original.exists {
			return errors.New("Kimi Code config changed after planning")
		}
		current = nil
	} else if err != nil {
		return fmt.Errorf("re-read Kimi Code config: %w", err)
	} else if !plan.original.exists {
		return errors.New("Kimi Code config changed after planning")
	}
	if sha256.Sum256(current) != plan.original.hash {
		return errors.New("Kimi Code config changed after planning")
	}
	mode := plan.original.mode
	if !plan.original.exists {
		mode = 0o600
	}
	if err := atomicfile.Write(plan.Path, plan.candidate.contents, mode); err != nil {
		return fmt.Errorf("write Kimi Code config: %w", err)
	}
	return nil
}

func (plan Plan) GoString() string {
	return fmt.Sprintf("kimicode.Plan{Path:%q, Write:%t, Changes:%#v, Conflicts:%#v, Protected:%#v}", plan.Path, plan.Write, plan.Changes, plan.Conflicts, plan.Protected)
}
