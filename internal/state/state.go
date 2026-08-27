package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/Mtrya/llmloot/internal/atomicfile"
)

const SchemaVersion = 1

type State struct {
	SchemaVersion                  int                    `json:"schema_version"`
	LLMlootVersion                 string                 `json:"llmloot_version,omitempty"`
	Sources                        map[string]SourceState `json:"sources,omitempty"`
	Jobs                           map[string]JobState    `json:"jobs,omitempty"`
	Targets                        map[string]TargetState `json:"targets,omitempty"`
	LastSuccessfulScheduleBoundary *time.Time             `json:"last_successful_schedule_boundary,omitempty"`
	Scheduler                      *SchedulerState        `json:"scheduler,omitempty"`
}

type SourceState struct {
	FirstSeen map[string]time.Time `json:"first_seen,omitempty"`
}

type JobState struct {
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Outcome     string     `json:"outcome,omitempty"`
	SelectedIDs []string   `json:"selected_ids,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type TargetState struct {
	Path      string                       `json:"path"`
	Providers map[string]ProviderOwnership `json:"providers,omitempty"`
	Models    map[string]ModelOwnership    `json:"models,omitempty"`
}

type ProviderOwnership struct {
	Fields map[string]string `json:"fields"`
}

type ModelOwnership struct {
	Source string            `json:"source"`
	Job    string            `json:"job"`
	Fields map[string]string `json:"fields"`
}

type SchedulerState struct {
	Kind           string `json:"kind"`
	Identifier     string `json:"identifier"`
	ExecutablePath string `json:"executable_path"`
	LocalTime      string `json:"local_time"`
	LastStatus     string `json:"last_status"`
}

func Load(path string) (State, error) {
	result, _, err := Read(path)
	return result, err
}

func Read(path string) (State, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("open state: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var result State
	if err := decoder.Decode(&result); err != nil {
		return State{}, true, fmt.Errorf("decode state: %w", err)
	}
	if result.SchemaVersion != SchemaVersion {
		return State{}, true, fmt.Errorf("unsupported state schema_version %d", result.SchemaVersion)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return State{}, true, errors.New("decode state: multiple JSON values")
		}
		return State{}, true, fmt.Errorf("decode trailing state data: %w", err)
	}
	result.initializeMaps()
	return result, true, nil
}

func (state State) FirstSeen(sourceName, modelID string) time.Time {
	return state.Sources[sourceName].FirstSeen[modelID]
}

func New() State {
	result := State{SchemaVersion: SchemaVersion}
	result.initializeMaps()
	return result
}

func (state *State) initializeMaps() {
	if state.Sources == nil {
		state.Sources = make(map[string]SourceState)
	}
	if state.Jobs == nil {
		state.Jobs = make(map[string]JobState)
	}
	if state.Targets == nil {
		state.Targets = make(map[string]TargetState)
	}
}

func (state *State) Observe(sourceName string, modelIDs []string, observedAt time.Time) {
	state.initializeMaps()
	sourceState := state.Sources[sourceName]
	if sourceState.FirstSeen == nil {
		sourceState.FirstSeen = make(map[string]time.Time)
	}
	for _, modelID := range modelIDs {
		if sourceState.FirstSeen[modelID].IsZero() {
			sourceState.FirstSeen[modelID] = observedAt.UTC()
		}
	}
	state.Sources[sourceName] = sourceState
}

func (state *State) RecordJob(name, outcome string, selectedIDs []string, compactError string, attemptedAt time.Time) {
	state.initializeMaps()
	attemptedAt = attemptedAt.UTC()
	jobState := state.Jobs[name]
	jobState.LastAttempt = &attemptedAt
	jobState.Outcome = outcome
	jobState.Error = compactError
	jobState.SelectedIDs = append([]string(nil), selectedIDs...)
	sort.Strings(jobState.SelectedIDs)
	if outcome != "error" {
		jobState.LastSuccess = &attemptedAt
	}
	state.Jobs[name] = jobState
}

func (state *State) SatisfyScheduleBoundary(boundary time.Time) {
	boundary = boundary.UTC()
	state.LastSuccessfulScheduleBoundary = &boundary
}

func Save(path string, current State) error {
	current.initializeMaps()
	contents, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	contents = append(contents, '\n')
	if err := atomicfile.Write(path, contents, 0o600); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
