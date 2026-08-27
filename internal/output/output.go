package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/Mtrya/llmloot/internal/app"
)

func JSON(writer io.Writer, result app.SyncResult) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

func Human(writer io.Writer, result app.SyncResult) error {
	if _, err := fmt.Fprintf(writer, "sync dry-run: %s\n", result.Outcome); err != nil {
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
	return nil
}
