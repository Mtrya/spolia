package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Mtrya/spolia/internal/app"
)

func JSON(writer io.Writer, result app.SyncResult) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

func Human(writer io.Writer, result app.SyncResult) error {
	operation := result.Operation
	if result.DryRun {
		operation += " dry-run"
	}
	if _, err := fmt.Fprintf(writer, "%s: %s\n", operation, result.Outcome); err != nil {
		return err
	}
	for _, job := range result.Jobs {
		if _, err := fmt.Fprintf(writer, "%s (%s -> %s): %s\n", job.Name, job.Source, job.Target, job.Outcome); err != nil {
			return err
		}
		if job.Error != "" {
			if _, err := fmt.Fprintf(writer, "  error: %s\n", job.Error); err != nil {
				return err
			}
		}
		for _, selected := range job.Selected {
			if _, err := fmt.Fprintf(writer, "  %s  %s  %s\n", selected.Class, selected.ID, selected.DisplayName); err != nil {
				return err
			}
		}
		if len(job.ExclusionSummary) > 0 {
			keys := make([]string, 0, len(job.ExclusionSummary))
			for key := range job.ExclusionSummary {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if _, err := fmt.Fprint(writer, "  excluded:"); err != nil {
				return err
			}
			for _, key := range keys {
				if _, err := fmt.Fprintf(writer, " %s=%d", key, job.ExclusionSummary[key]); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(writer); err != nil {
				return err
			}
		}
	}
	for _, targetPlan := range result.TargetPlans {
		if _, err := fmt.Fprintf(writer, "target %s: write=%t\n", targetPlan.Target, targetPlan.Write); err != nil {
			return err
		}
		for _, change := range targetPlan.Changes {
			if _, err := fmt.Fprintf(writer, "  %s %s %s\n", change.Action, change.Kind, change.ID); err != nil {
				return err
			}
		}
		for _, protected := range targetPlan.Protected {
			if _, err := fmt.Fprintf(writer, "  protected model %s (%s)\n", protected.ID, strings.Join(protected.References, ", ")); err != nil {
				return err
			}
		}
		for _, conflict := range targetPlan.Conflicts {
			if _, err := fmt.Fprintf(writer, "  conflict %s %s: %s\n", conflict.Kind, conflict.ID, conflict.Reason); err != nil {
				return err
			}
		}
	}
	if result.Schedule != nil {
		if _, err := fmt.Fprintf(writer, "scheduler %s: %s\n", result.Schedule.Identifier, result.Schedule.Status); err != nil {
			return err
		}
		if result.Schedule.Error != "" {
			if _, err := fmt.Fprintf(writer, "  error: %s\n", result.Schedule.Error); err != nil {
				return err
			}
		}
	}
	return nil
}
