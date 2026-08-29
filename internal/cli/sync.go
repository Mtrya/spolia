package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mtrya/spolia/internal/app"
	"github.com/Mtrya/spolia/internal/config"
	"github.com/Mtrya/spolia/internal/output"
	"github.com/Mtrya/spolia/internal/schedule"
	"github.com/Mtrya/spolia/internal/state"
	"github.com/Mtrya/spolia/internal/target/kimicode"
)

type syncOptions struct {
	job    string
	dryRun bool
	json   bool
	quiet  bool
	ifDue  bool
	help   bool
}

func runSync(ctx context.Context, arguments []string, stdout, stderr io.Writer) (exitCode int) {
	options, err := parseSyncOptions(arguments)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printSyncUsage(stderr)
		return exitUsage
	}
	if options.help {
		printSyncUsage(stdout)
		return exitOK
	}
	if options.quiet && options.json {
		fmt.Fprintln(stderr, "--quiet and --json are mutually exclusive")
		return exitUsage
	}
	if options.ifDue && options.job != "" {
		fmt.Fprintln(stderr, "--if-due cannot be used with a single job")
		return exitUsage
	}

	configPath, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	configuration, notConfigured, err := loadConfiguration(configPath)
	if notConfigured {
		fmt.Fprintln(stderr, "spolia is not set up yet; run spolia setup")
		return exitFailure
	}
	if err != nil {
		fmt.Fprintf(stderr, "%v\nFix or remove %s, or run spolia setup to recreate it.\n", err, configPath)
		return exitFailure
	}
	jobs, err := configuration.EnabledJobs(options.job)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	targetName, err := onlyKimiTarget(configuration)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if options.ifDue {
		preflightState, exists, readErr := state.Read(config.StatePath(configPath))
		if readErr != nil {
			fmt.Fprintln(stderr, readErr)
			return exitFailure
		}
		if !exists {
			fmt.Fprintln(stderr, "ownership state is missing; run spolia setup before sync")
			return exitFailure
		}
		due, dueErr := syncIsDue(configuration, preflightState)
		if dueErr != nil {
			fmt.Fprintln(stderr, dueErr)
			return exitFailure
		}
		if !due {
			return writeNotDue(stdout, stderr, options, configuration, preflightState)
		}
	}
	lock, err := state.AcquireLock(config.LockPath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Fprintln(stderr, err)
			exitCode = exitFailure
		}
	}()
	currentState, exists, err := state.Read(config.StatePath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if !exists {
		fmt.Fprintln(stderr, "ownership state is missing; run spolia setup before sync")
		return exitFailure
	}
	if options.ifDue {
		due, dueErr := syncIsDue(configuration, currentState)
		if dueErr != nil {
			fmt.Fprintln(stderr, dueErr)
			return exitFailure
		}
		if !due {
			return writeNotDue(stdout, stderr, options, configuration, currentState)
		}
	}
	installation, err := kimicode.Discover(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	document, err := kimicode.Load(installation.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	providers, err := providersForJobs(configuration, jobs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	credentials := kimicode.Credentials{Document: document, Providers: providers}
	result := application(credentials).Plan(ctx, configuration, currentState, options.job, options.dryRun)
	requirements := providerRequirements(ctx, credentials, providers)
	targetPlan := kimicode.PlanSync(document, currentState.Targets[targetName], requirements, selections(result, providers))
	attachTargetPlan(&result, targetName, configuration.Targets[targetName].Adapter, targetPlan)
	if len(targetPlan.Conflicts) == 0 {
		if options.dryRun {
			err = targetPlan.Validate(ctx, installation)
		} else {
			err = targetPlan.Apply(ctx, installation)
		}
		if err != nil {
			result.Outcome = "failure"
			result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(targetName, err))
		}
	}
	if !options.dryRun && len(targetPlan.Conflicts) == 0 && err == nil {
		updateState(&currentState, result, targetName, targetPlan.Ownership)
		if boundaryErr := satisfyBoundaryAfterFullSuccess(&currentState, configuration, result.Outcome, options.job, time.Now()); boundaryErr != nil {
			fmt.Fprintln(stderr, boundaryErr)
			return exitFailure
		}
		if saveErr := state.Save(config.StatePath(configPath), currentState); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)
			return exitFailure
		}
	}
	if !options.quiet {
		if options.json {
			err = output.JSON(stdout, result)
		} else {
			err = output.Human(stdout, result)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
	}
	if result.Outcome == "success" {
		return exitOK
	}
	return exitFailure
}

func syncIsDue(configuration config.Config, currentState state.State) (bool, error) {
	if !configuration.Schedule.Enabled {
		return false, nil
	}
	due, _, err := schedule.IsDue(time.Now().In(time.Local), configuration.Schedule.LocalTime, currentState.LastSuccessfulScheduleBoundary)
	return due, err
}

func writeNotDue(stdout, stderr io.Writer, options syncOptions, configuration config.Config, currentState state.State) int {
	if options.quiet {
		return exitOK
	}
	now := time.Now().In(time.Local)
	next := time.Time{}
	if boundary, err := schedule.CurrentBoundary(now, configuration.Schedule.LocalTime); err == nil {
		next = boundary.AddDate(0, 0, 1)
	}
	result := app.SyncResult{SchemaVersion: app.ResultSchemaVersion, Operation: "sync", Outcome: "not_due", Jobs: []app.JobResult{}, TargetPlans: []app.TargetPlan{}}
	// The boundary records which scheduled slot was satisfied, not when the
	// check ran; the jobs' success timestamps carry the real time.
	result.LastCheck = lastSuccessfulCheck(currentState)
	if result.LastCheck == nil {
		result.LastCheck = currentState.LastSuccessfulScheduleBoundary
	}
	if !next.IsZero() {
		result.NextCheck = &next
	}
	if options.json {
		if err := output.JSON(stdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
		return exitOK
	}
	if _, err := fmt.Fprintln(stdout, "sync: not_due"); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if result.LastCheck != nil {
		fmt.Fprintf(stdout, "last check %s; ", result.LastCheck.Local().Format("2006-01-02 15:04"))
	}
	if result.NextCheck != nil {
		fmt.Fprintf(stdout, "next check %s\n", result.NextCheck.Local().Format("2006-01-02 15:04"))
	}
	return exitOK
}

// lastSuccessfulCheck returns the most recent per-job success timestamp,
// which records when a check actually ran rather than the schedule boundary
// it satisfied.
func lastSuccessfulCheck(currentState state.State) *time.Time {
	var latest *time.Time
	for _, jobState := range currentState.Jobs {
		if jobState.LastSuccess != nil && (latest == nil || jobState.LastSuccess.After(*latest)) {
			latest = jobState.LastSuccess
		}
	}
	return latest
}

func appTargetError(targetName string, err error) app.TargetConflict {
	return app.TargetConflict{Kind: "target", ID: targetName, Reason: compactError(err.Error())}
}

func parseSyncOptions(arguments []string) (syncOptions, error) {
	var result syncOptions
	for _, argument := range arguments {
		switch argument {
		case "--dry-run":
			result.dryRun = true
		case "--json":
			result.json = true
		case "--quiet":
			result.quiet = true
		case "--if-due":
			result.ifDue = true
		case "--help", "-h":
			result.help = true
		default:
			if strings.HasPrefix(argument, "-") {
				return syncOptions{}, fmt.Errorf("unknown sync option %q", argument)
			}
			if result.job != "" {
				return syncOptions{}, fmt.Errorf("sync accepts at most one job")
			}
			result.job = argument
		}
	}
	return result, nil
}

func printSyncUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: spolia sync [job] [--dry-run] [--if-due] [--quiet] [--json]

Refreshes the model catalogs and updates the models spolia manages in Kimi Code.

  [job]       sync only one job (for example openrouter-kimi-code)
  --dry-run   show what would change without writing anything
  --if-due    skip when the daily scheduled check already ran (used by the schedule)
  --quiet     print nothing; only the exit code reports the result
  --json      print the result as JSON

Example: spolia sync --dry-run`)
}
