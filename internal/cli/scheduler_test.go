package cli

import (
	"context"
	"testing"
	"time"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/schedule"
	"github.com/Mtrya/llmloot/internal/state"
)

func TestOnlySuccessfulFullSyncSatisfiesScheduleBoundary(t *testing.T) {
	t.Parallel()
	configuration := config.Default()
	completedAt := time.Date(2026, 8, 27, 10, 0, 0, 0, time.Local)

	for _, test := range []struct {
		name         string
		enabled      bool
		outcome      string
		requestedJob string
		satisfied    bool
	}{
		{name: "full success", enabled: true, outcome: "success", satisfied: true},
		{name: "partial failure", enabled: true, outcome: "partial_failure"},
		{name: "full failure", enabled: true, outcome: "failure"},
		{name: "single job", enabled: true, outcome: "success", requestedJob: "openrouter-kimi-code"},
		{name: "schedule disabled", outcome: "success", satisfied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := state.New()
			configuration.Schedule.Enabled = test.enabled
			if err := satisfyBoundaryAfterFullSuccess(&current, configuration, test.outcome, test.requestedJob, completedAt); err != nil {
				t.Fatal(err)
			}
			if (current.LastSuccessfulScheduleBoundary != nil) != test.satisfied {
				t.Fatalf("boundary = %#v", current.LastSuccessfulScheduleBoundary)
			}
		})
	}
}

func TestDoctorRequiresSchedulerOwnershipWhenEnabled(t *testing.T) {
	t.Parallel()
	result := doctorResult{SchemaVersion: 1, Operation: "doctor", Outcome: "healthy"}
	checkScheduler(context.Background(), config.Default(), state.New(), true, &result)
	if result.Outcome != "unhealthy" || len(result.Checks) != 1 || result.Checks[0].Name != "scheduler" || result.Checks[0].Status != "error" {
		t.Fatalf("doctor result = %#v", result)
	}
}

func TestRepeatedWakeIsNotDueAfterBoundaryIsSatisfied(t *testing.T) {
	t.Parallel()
	configuration := config.Default()
	current := state.New()
	completedAt := time.Date(2026, 8, 27, 10, 0, 0, 0, time.Local)
	if err := satisfyBoundaryAfterFullSuccess(&current, configuration, "success", "", completedAt); err != nil {
		t.Fatal(err)
	}
	due, _, err := schedule.IsDue(completedAt.Add(time.Hour), configuration.Schedule.LocalTime, current.LastSuccessfulScheduleBoundary)
	if err != nil || due {
		t.Fatalf("due=%t err=%v", due, err)
	}
}
