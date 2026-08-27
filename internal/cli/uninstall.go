package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/schedule"
	"github.com/Mtrya/llmloot/internal/state"
	"github.com/Mtrya/llmloot/internal/target/kimicode"
)

type uninstallOptions struct {
	dryRun bool
	yes    bool
	json   bool
	help   bool
}

type uninstallResult struct {
	SchemaVersion int                  `json:"schema_version"`
	Operation     string               `json:"operation"`
	DryRun        bool                 `json:"dry_run"`
	Outcome       string               `json:"outcome"`
	TargetPlan    app.TargetPlan       `json:"target_plan"`
	Scheduler     *schedule.Inspection `json:"scheduler,omitempty"`
	Artifacts     []string             `json:"artifacts"`
}

func runUninstall(ctx context.Context, arguments []string, stdout, stderr io.Writer) (exitCode int) {
	options, err := parseUninstallOptions(arguments)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printUninstallUsage(stderr)
		return 2
	}
	if options.help {
		printUninstallUsage(stdout)
		return 0
	}
	if options.json && !options.dryRun && !options.yes {
		fmt.Fprintln(stderr, "--json requires --yes when uninstalling")
		return 2
	}
	configPath, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	configuration, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	targetName, err := onlyKimiTarget(configuration)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	lock, err := state.AcquireLock(config.LockPath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Fprintln(stderr, err)
			exitCode = 1
		}
	}()
	currentState, exists, err := state.Read(config.StatePath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !exists {
		fmt.Fprintln(stderr, "ownership state is missing; target cleanup cannot continue safely")
		return 1
	}
	installation, err := kimicode.Discover(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	targetPath := installation.ConfigPath
	if stored := currentState.Targets[targetName].Path; stored != "" {
		targetPath = stored
	}
	document, err := kimicode.Load(targetPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan := kimicode.PlanUninstall(document, currentState.Targets[targetName])
	operation := app.SyncResult{}
	attachTargetPlan(&operation, targetName, configuration.Targets[targetName].Adapter, plan)
	result := uninstallResult{SchemaVersion: 1, Operation: "uninstall", DryRun: options.dryRun, Outcome: "success", TargetPlan: operation.TargetPlans[0], Artifacts: []string{configPath, config.StatePath(configPath)}}
	var schedulerManager schedule.Manager
	if currentState.Scheduler != nil {
		var schedulerInspection schedule.Inspection
		schedulerManager, schedulerInspection, err = inspectStoredScheduler(ctx, *currentState.Scheduler)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		result.Scheduler = &schedulerInspection
		result.Artifacts = append(schedulerInspection.Artifacts, result.Artifacts...)
		if schedulerInspection.Installed && (!schedulerInspection.Managed || !schedulerInspection.Matches) {
			result.Outcome = "failure"
		}
	}
	if len(plan.Conflicts) > 0 {
		result.Outcome = "failure"
	} else if err := plan.Validate(ctx, installation); err != nil {
		result.Outcome = "failure"
		result.TargetPlan.Conflicts = append(result.TargetPlan.Conflicts, appTargetError(err))
	}
	if result.Outcome != "success" {
		if err := writeUninstall(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	if options.dryRun {
		if err := writeUninstall(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if !options.yes {
		if err := writeUninstall(stdout, result, false); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		confirmed, err := askYesNo(bufio.NewReader(os.Stdin), stdout, "Remove these llmloot-owned artifacts?", false)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !confirmed {
			fmt.Fprintln(stderr, "uninstall cancelled")
			return 1
		}
	}
	if result.Scheduler != nil {
		if _, err := schedulerManager.Remove(ctx); err != nil {
			result.Outcome = "failure"
			if options.yes {
				_ = writeUninstall(stdout, result, options.json)
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := plan.Apply(ctx, installation); err != nil {
		result.Outcome = "failure"
		result.TargetPlan.Conflicts = append(result.TargetPlan.Conflicts, appTargetError(err))
		if options.yes {
			_ = writeUninstall(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := removeIfExists(configPath); err != nil {
		result.Outcome = "failure"
		if options.yes {
			_ = writeUninstall(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := removeIfExists(config.StatePath(configPath)); err != nil {
		result.Outcome = "failure"
		if options.yes {
			_ = writeUninstall(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if options.yes {
		if err := writeUninstall(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func writeUninstall(writer io.Writer, result uninstallResult, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(result)
	}
	if _, err := fmt.Fprintf(writer, "uninstall %s: %s\n", map[bool]string{true: "dry-run", false: "plan"}[result.DryRun], result.Outcome); err != nil {
		return err
	}
	for _, change := range result.TargetPlan.Changes {
		if _, err := fmt.Fprintf(writer, "  %s %s %s\n", change.Action, change.Kind, change.ID); err != nil {
			return err
		}
	}
	for _, conflict := range result.TargetPlan.Conflicts {
		if _, err := fmt.Fprintf(writer, "  conflict %s %s: %s\n", conflict.Kind, conflict.ID, conflict.Reason); err != nil {
			return err
		}
	}
	if result.Scheduler != nil {
		if _, err := fmt.Fprintf(writer, "  scheduler %s %s\n", result.Scheduler.Identifier, result.Scheduler.Status); err != nil {
			return err
		}
	}
	for _, artifact := range result.Artifacts {
		if _, err := fmt.Fprintf(writer, "  remove %s\n", artifact); err != nil {
			return err
		}
	}
	return nil
}

func parseUninstallOptions(arguments []string) (uninstallOptions, error) {
	var result uninstallOptions
	for _, argument := range arguments {
		switch argument {
		case "--dry-run":
			result.dryRun = true
		case "--yes", "-y":
			result.yes = true
		case "--json":
			result.json = true
		case "--help", "-h":
			result.help = true
		default:
			return uninstallOptions{}, fmt.Errorf("unknown uninstall option %q", argument)
		}
	}
	return result, nil
}

func printUninstallUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: llmloot uninstall [--dry-run] [--yes] [--json]")
}
