package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const SchemaVersion = 1

type State struct {
	SchemaVersion int                    `json:"schema_version"`
	Sources       map[string]SourceState `json:"sources,omitempty"`
}

type SourceState struct {
	FirstSeen map[string]time.Time `json:"first_seen,omitempty"`
}

func Load(path string) (State, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{SchemaVersion: SchemaVersion}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("open state: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var result State
	if err := decoder.Decode(&result); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	if result.SchemaVersion != SchemaVersion {
		return State{}, fmt.Errorf("unsupported state schema_version %d", result.SchemaVersion)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return State{}, errors.New("decode state: multiple JSON values")
		}
		return State{}, fmt.Errorf("decode trailing state data: %w", err)
	}
	return result, nil
}

func (state State) FirstSeen(sourceName, modelID string) time.Time {
	return state.Sources[sourceName].FirstSeen[modelID]
}
