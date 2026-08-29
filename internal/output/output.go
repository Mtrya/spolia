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

// Human renders the full final report: operation detail plus the
// plain-language summary of what the user can do next.
func Human(writer io.Writer, result app.SyncResult) error {
	if err := Plan(writer, result); err != nil {
		return err
	}
	return Summary(writer, result)
}

// Plan renders only the operation detail: job outcomes, selections,
// exclusions, and target changes. It is used for previews before any change
// is applied, where the summary's "ready to use" language would be wrong.
func Plan(writer io.Writer, result app.SyncResult) error {
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
			if hint := catalogErrorHint(job); hint != "" {
				if _, err := fmt.Fprintf(writer, "  hint: %s\n", hint); err != nil {
					return err
				}
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
			if _, err := fmt.Fprint(writer, "  excluded: "); err != nil {
				return err
			}
			for index, key := range keys {
				if index > 0 {
					if _, err := fmt.Fprint(writer, ", "); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(writer, "%d %s", job.ExclusionSummary[key], exclusionPhrase(key)); err != nil {
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

// Summary renders the plain-language conclusion: whether models are usable,
// the exact command to try one, the actionable reason behind an empty result,
// and what happens next.
func Summary(writer io.Writer, result app.SyncResult) error {
	if result.Outcome == "not_due" {
		return nil
	}
	selected := selectedIDs(result)
	if result.Outcome != "failure" && result.Outcome != "partial_failure" {
		if len(selected) > 0 {
			if _, err := fmt.Fprintln(writer, "Models ready in Kimi Code:"); err != nil {
				return err
			}
			for _, id := range selected {
				if _, err := fmt.Fprintf(writer, "  %s\n", id); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(writer, "Try one now: kimi --model '%s'\n", selected[0]); err != nil {
				return err
			}
		} else {
			conclusion := "No matching models are available right now."
			if result.Operation == "setup" {
				conclusion = "Setup is complete, but no matching models are available right now."
			}
			if _, err := fmt.Fprintln(writer, conclusion); err != nil {
				return err
			}
			if disabled := freeModelsDisabled(result); disabled > 0 {
				if _, err := fmt.Fprintf(writer, "%d ordinary free models are available; rerun spolia setup and enable ordinary free models to use one.\n", disabled); err != nil {
					return err
				}
			}
			if nothingWritten(result) {
				if _, err := fmt.Fprintln(writer, "Kimi Code configuration unchanged."); err != nil {
					return err
				}
			}
		}
	}
	if result.Schedule != nil {
		switch {
		case result.Schedule.Status == "error" || result.Schedule.Error != "":
			if _, err := fmt.Fprintf(writer, "Daily scheduling failed: %s. Models are ready; rerun spolia setup --no-schedule or fix the cause.\n", result.Schedule.Error); err != nil {
				return err
			}
		case result.Schedule.Enabled && result.Schedule.Status != "disabled" && result.Schedule.Status != "removed":
			if _, err := fmt.Fprintf(writer, "Spolia will check again daily at %s.\n", result.Schedule.LocalTime); err != nil {
				return err
			}
		}
	}
	return nil
}

func selectedIDs(result app.SyncResult) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, job := range result.Jobs {
		for _, selected := range job.Selected {
			if !seen[selected.ID] {
				seen[selected.ID] = true
				ids = append(ids, selected.ID)
			}
		}
	}
	return ids
}

// freeModelsDisabled counts ordinary free models that were excluded only
// because the job policy did not opt into them.
func freeModelsDisabled(result app.SyncResult) int {
	total := 0
	for _, job := range result.Jobs {
		if !job.IncludeFree {
			total += job.ExclusionSummary["class_disabled"]
		}
	}
	return total
}

func nothingWritten(result app.SyncResult) bool {
	for _, targetPlan := range result.TargetPlans {
		if targetPlan.Write {
			return false
		}
	}
	return true
}

// catalogErrorHint translates common catalog fetch failures into the action
// that resolves them. The raw error stays on the detail line above.
func catalogErrorHint(job app.JobResult) string {
	switch {
	case strings.Contains(job.Error, "HTTP 401") || strings.Contains(job.Error, "HTTP 403"):
		if job.CredentialEnv != "" {
			return fmt.Sprintf("the %s API key was rejected; update the provider credential in Kimi Code or set %s and retry", job.Source, job.CredentialEnv)
		}
		return fmt.Sprintf("the %s API key was rejected; update the provider credential in Kimi Code and retry", job.Source)
	case strings.Contains(job.Error, "Timeout") || strings.Contains(job.Error, "deadline exceeded") || strings.Contains(job.Error, "no such host") || strings.Contains(job.Error, "connection refused"):
		return fmt.Sprintf("cannot reach %s; check network access and retry", job.Source)
	}
	return ""
}

var exclusionPhrases = map[string]string{
	"class_disabled":                  "ordinary free models not enabled",
	"context_below_minimum":           "below the minimum context",
	"expired":                         "expired",
	"invalid_context":                 "invalid context window",
	"malformed_price":                 "malformed pricing",
	"malformed_record":                "unreadable catalog record",
	"missing_required_price":          "missing pricing",
	"no_text_output":                  "no text output",
	"not_concrete_model":              "not a concrete model",
	"paid_model":                      "paid",
	"source_collision":                "kept from a higher-priority source",
	"stealth_price_invalid":           "stealth pricing invalid",
	"stealth_requires_free":           "stealth without free pricing",
	"tools_explicitly_unsupported":    "tools unsupported",
	"unsupported_protocol":            "unsupported protocol",
	"unsupported_required_price_unit": "unsupported pricing unit",
}

func exclusionPhrase(code string) string {
	if phrase, ok := exclusionPhrases[code]; ok {
		return phrase
	}
	return code
}
