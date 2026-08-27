package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Mtrya/spolia/internal/config"
	"github.com/Mtrya/spolia/internal/schedule"
	"github.com/Mtrya/spolia/internal/state"
)

func currentScheduleDefinition(localTime string) (schedule.Definition, error) {
	if os.Getenv("SPOLIA_HOME") != "" {
		return schedule.Definition{}, fmt.Errorf("native scheduling requires the standard per-user spolia home; unset SPOLIA_HOME or use --no-schedule")
	}
	executable, err := os.Executable()
	if err != nil {
		return schedule.Definition{}, fmt.Errorf("resolve spolia executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return schedule.Definition{}, fmt.Errorf("resolve absolute spolia executable: %w", err)
	}
	definition := schedule.Definition{Executable: executable, LocalTime: localTime}
	if err := schedule.ValidateDefinition(definition); err != nil {
		return schedule.Definition{}, err
	}
	return definition, nil
}

func storedSchedule(current state.SchedulerState) (schedule.Manager, schedule.Definition, error) {
	manager, err := schedule.New(current.Identifier)
	if err != nil {
		return schedule.Manager{}, schedule.Definition{}, err
	}
	if current.Kind != manager.Kind() {
		return schedule.Manager{}, schedule.Definition{}, fmt.Errorf("scheduler state kind %q does not match this platform's %q", current.Kind, manager.Kind())
	}
	definition := schedule.Definition{Executable: current.ExecutablePath, LocalTime: current.LocalTime}
	if err := schedule.ValidateDefinition(definition); err != nil {
		return schedule.Manager{}, schedule.Definition{}, fmt.Errorf("invalid scheduler ownership state: %w", err)
	}
	return manager, definition, nil
}

func inspectStoredScheduler(ctx context.Context, current state.SchedulerState) (schedule.Manager, schedule.Inspection, error) {
	manager, definition, err := storedSchedule(current)
	if err != nil {
		return schedule.Manager{}, schedule.Inspection{}, err
	}
	inspection, err := manager.Inspect(ctx, definition)
	if err != nil {
		return schedule.Manager{}, schedule.Inspection{}, err
	}
	return manager, inspection, nil
}

func schedulerState(definition schedule.Definition, inspection schedule.Inspection) *state.SchedulerState {
	return &state.SchedulerState{
		Kind:           inspection.Kind,
		Identifier:     inspection.Identifier,
		ExecutablePath: definition.Executable,
		LocalTime:      definition.LocalTime,
		LastStatus:     inspection.Status,
	}
}

func reconcileConfiguredScheduler(ctx context.Context, statePath string, configuration config.Config, current *state.State) (schedule.Inspection, error) {
	if current.Scheduler != nil {
		manager, inspection, err := inspectStoredScheduler(ctx, *current.Scheduler)
		if err != nil {
			return schedule.Inspection{}, err
		}
		if inspection.Installed && (!inspection.Managed || !inspection.Matches) {
			return inspection, fmt.Errorf("scheduler artifact %q changed since spolia last wrote it", inspection.Identifier)
		}
		if !configuration.Schedule.Enabled {
			if _, err := manager.Remove(ctx); err != nil {
				return inspection, err
			}
			current.Scheduler = nil
			if err := state.Save(statePath, *current); err != nil {
				return inspection, err
			}
			inspection.Enabled = false
			inspection.Installed = false
			inspection.Status = "removed"
			return inspection, nil
		}
		if current.Scheduler.Identifier != schedule.DefaultIdentifier {
			return inspection, fmt.Errorf("scheduler state uses unexpected identifier %q", current.Scheduler.Identifier)
		}
	} else if !configuration.Schedule.Enabled {
		return schedule.Inspection{Kind: schedule.Current().Kind(), Identifier: schedule.DefaultIdentifier, Status: "disabled"}, nil
	}

	manager := schedule.Current()
	definition, err := currentScheduleDefinition(configuration.Schedule.LocalTime)
	if err != nil {
		return schedule.Inspection{Kind: manager.Kind(), Identifier: manager.Identifier(), Status: "error"}, err
	}
	if current.Scheduler == nil {
		inspection, err := manager.Inspect(ctx, definition)
		if err != nil {
			return schedule.Inspection{}, err
		}
		if inspection.Installed && !inspection.Managed {
			return inspection, fmt.Errorf("scheduler identifier %q is already used by an unmanaged artifact", inspection.Identifier)
		}
	}
	pending := schedule.Inspection{Kind: manager.Kind(), Identifier: manager.Identifier(), Status: "installing"}
	current.Scheduler = schedulerState(definition, pending)
	if err := state.Save(statePath, *current); err != nil {
		return schedule.Inspection{}, err
	}
	inspection, err := manager.Install(ctx, definition)
	if err != nil {
		current.Scheduler.LastStatus = "error"
		_ = state.Save(statePath, *current)
		return inspection, err
	}
	current.Scheduler = schedulerState(definition, inspection)
	if err := state.Save(statePath, *current); err != nil {
		return inspection, err
	}
	return inspection, nil
}

func satisfyBoundaryAfterFullSuccess(current *state.State, configuration config.Config, outcome, requestedJob string, completedAt time.Time) error {
	if requestedJob != "" || outcome != "success" {
		return nil
	}
	boundary, err := schedule.CurrentBoundary(completedAt.In(time.Local), configuration.Schedule.LocalTime)
	if err != nil {
		return err
	}
	current.SatisfyScheduleBoundary(boundary)
	return nil
}
