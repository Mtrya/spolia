package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/credential"
	"github.com/Mtrya/llmloot/internal/output"
	"github.com/Mtrya/llmloot/internal/source"
	"github.com/Mtrya/llmloot/internal/source/openrouter"
	"github.com/Mtrya/llmloot/internal/source/zenmux"
	"github.com/Mtrya/llmloot/internal/state"
)

const Version = "0.1.0-dev"

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		printUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "sync":
		return runSync(ctx, arguments[1:], stdout, stderr)
	case "setup", "doctor", "uninstall":
		fmt.Fprintf(stderr, "%s is not implemented yet\n", arguments[0])
		return 1
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		printUsage(stderr)
		return 2
	}
}

type syncOptions struct {
	job    string
	dryRun bool
	json   bool
	quiet  bool
	ifDue  bool
	help   bool
}

func runSync(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
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
		fmt.Fprintln(stderr, "--if-due requires scheduling support from a later implementation phase")
		return 1
	}
	if !options.dryRun {
		fmt.Fprintln(stderr, "sync writes are not implemented yet; use --dry-run")
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
	if _, err := configuration.EnabledJobs(options.job); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	lock, err := state.AcquireLock(config.LockPath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	currentState, err := state.Load(config.StatePath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintln(stderr, releaseErr)
		}
		return 1
	}

	client := &http.Client{Timeout: 30 * time.Second}
	application := app.Application{
		Adapters: map[string]source.Adapter{
			"openrouter": openrouter.New(client),
			"zenmux":     zenmux.New(client),
		},
		Credentials: credential.Environment{},
	}
	result := application.DryRun(ctx, configuration, currentState, options.job)
	if err := lock.Release(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
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

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: llmloot <setup|sync|doctor|uninstall|help|version>")
}

func printSyncUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: llmloot sync [job] [--dry-run] [--if-due] [--quiet] [--json]")
}
