package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/output"
	"github.com/Mtrya/llmloot/internal/state"
	"github.com/Mtrya/llmloot/internal/target/kimicode"
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
		return 2
	}
	if options.help {
		printSyncUsage(stdout)
		return 0
	}
	if options.quiet && options.json {
		fmt.Fprintln(stderr, "--quiet and --json are mutually exclusive")
		return 2
	}
	if options.ifDue {
		fmt.Fprintln(stderr, "native scheduling is not available yet")
		return 1
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
	jobs, err := configuration.EnabledJobs(options.job)
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
		fmt.Fprintln(stderr, "ownership state is missing; run llmloot setup before sync")
		return 1
	}
	installation, err := kimicode.Discover(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	document, err := kimicode.Load(installation.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	providers, err := providersForJobs(configuration, jobs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
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
			result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(err))
		}
	}
	if !options.dryRun && len(targetPlan.Conflicts) == 0 && err == nil {
		updateState(&currentState, result, targetName, targetPlan.Ownership)
		if saveErr := state.Save(config.StatePath(configPath), currentState); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)
			return 1
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
			return 1
		}
	}
	if result.Outcome == "success" {
		return 0
	}
	return 1
}

func appTargetError(err error) app.TargetConflict {
	return app.TargetConflict{Kind: "target", ID: "kimi-code", Reason: compactError(err.Error())}
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
	fmt.Fprintln(writer, "usage: llmloot sync [job] [--dry-run] [--if-due] [--quiet] [--json]")
}
