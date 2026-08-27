package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/output"
	"github.com/Mtrya/llmloot/internal/state"
	"github.com/Mtrya/llmloot/internal/target/kimicode"
	"golang.org/x/term"
)

type setupOptions struct {
	yes        bool
	json       bool
	noSchedule bool
	help       bool
}

func runSetup(ctx context.Context, arguments []string, stdout, stderr io.Writer) (exitCode int) {
	options, err := parseSetupOptions(arguments)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printSetupUsage(stderr)
		return 2
	}
	if options.help {
		printSetupUsage(stdout)
		return 0
	}
	if options.json && !options.yes {
		fmt.Fprintln(stderr, "--json requires --yes")
		return 2
	}
	configPath, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	configuration, err := config.Load(configPath)
	if errors.Is(err, os.ErrNotExist) {
		configuration = config.Default()
	} else if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if options.noSchedule {
		configuration.Schedule.Enabled = false
	}
	if !options.yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(stderr, "interactive setup requires a terminal; use --yes for the current configuration or stealth-only defaults")
			return 1
		}
		configuration, err = interviewConfiguration(configuration, options.noSchedule, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := configuration.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	jobs, err := configuration.EnabledJobs("")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(jobs) == 0 {
		fmt.Fprintln(stderr, "setup requires at least one enabled source")
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
	currentState, _, err := state.Read(config.StatePath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
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
	bootstrap, err := setupCredentials(configuration, document, providers, options.yes, stdout)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	credentials := kimicode.Credentials{Document: document, Providers: providers, Bootstrap: bootstrap}
	result := application(credentials).Plan(ctx, configuration, currentState, "", false)
	result.Operation = "setup"
	requirements := providerRequirements(ctx, credentials, providers)
	targetPlan := kimicode.PlanSync(document, currentState.Targets[targetName], requirements, selections(result, providers))
	attachTargetPlan(&result, targetName, configuration.Targets[targetName].Adapter, targetPlan)
	if len(targetPlan.Conflicts) == 0 && result.Outcome != "failure" {
		if err := targetPlan.Validate(ctx, installation); err != nil {
			result.Outcome = "failure"
			result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(err))
		}
	}
	if result.Outcome == "failure" || len(targetPlan.Conflicts) > 0 {
		if err := writeSetupResult(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	if !options.yes {
		if err := output.Human(stdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		confirmed, err := askYesNo(bufio.NewReader(os.Stdin), stdout, "Apply these changes?", false)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !confirmed {
			fmt.Fprintln(stderr, "setup cancelled")
			return 1
		}
	}
	if err := targetPlan.Apply(ctx, installation); err != nil {
		result.Outcome = "failure"
		result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(err))
		if options.yes {
			_ = writeSetupResult(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, compactError(err.Error()))
		return 1
	}
	updateState(&currentState, result, targetName, targetPlan.Ownership)
	if err := satisfyBoundaryAfterFullSuccess(&currentState, configuration, result.Outcome, "", time.Now()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := state.Save(config.StatePath(configPath), currentState); err != nil {
		result.Outcome = "failure"
		result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(err))
		if options.yes {
			_ = writeSetupResult(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := config.Save(configPath, configuration); err != nil {
		result.Outcome = "failure"
		result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(err))
		if options.yes {
			_ = writeSetupResult(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	inspection, err := reconcileConfiguredScheduler(ctx, config.StatePath(configPath), configuration, &currentState)
	result.Schedule = &app.SchedulePlan{
		Enabled:    configuration.Schedule.Enabled,
		Kind:       inspection.Kind,
		Identifier: inspection.Identifier,
		LocalTime:  configuration.Schedule.LocalTime,
		Status:     inspection.Status,
		Artifacts:  inspection.Artifacts,
	}
	if currentState.Scheduler != nil {
		result.Schedule.Executable = currentState.Scheduler.ExecutablePath
	}
	if err != nil {
		result.Outcome = "failure"
		result.Schedule.Status = "error"
		result.Schedule.Error = compactError(err.Error())
		if options.yes {
			_ = writeSetupResult(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, compactError(err.Error()))
		return 1
	}
	if options.yes {
		if err := writeSetupResult(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if result.Outcome == "success" {
		return 0
	}
	return 1
}

func writeSetupResult(writer io.Writer, result app.SyncResult, asJSON bool) error {
	if asJSON {
		return output.JSON(writer, result)
	}
	return output.Human(writer, result)
}

func setupCredentials(configuration config.Config, document kimicode.Document, providers map[string]kimicode.ProviderSpec, nonInteractive bool, writer io.Writer) (map[string]string, error) {
	bootstrap := make(map[string]string)
	sourceNames := make([]string, 0, len(providers))
	for sourceName := range providers {
		sourceNames = append(sourceNames, sourceName)
	}
	sort.Strings(sourceNames)
	for _, sourceName := range sourceNames {
		provider := providers[sourceName]
		inspection := document.Provider(provider)
		if inspection.Exists && !inspection.Compatible {
			return nil, fmt.Errorf("Kimi Code provider %q is incompatible: %s", provider.ID, inspection.Reason)
		}
		if inspection.CredentialExists {
			continue
		}
		environmentName := configuration.Sources[sourceName].CredentialEnv
		if credential := os.Getenv(environmentName); credential != "" {
			bootstrap[sourceName] = credential
			continue
		}
		if nonInteractive || !term.IsTerminal(int(os.Stdin.Fd())) {
			return nil, fmt.Errorf("source %q needs a credential in Kimi Code or %s", sourceName, environmentName)
		}
		fmt.Fprintf(writer, "Enter the API key for %s: ", sourceName)
		credential, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(writer)
		if err != nil {
			return nil, fmt.Errorf("read hidden credential for source %q: %w", sourceName, err)
		}
		if len(credential) == 0 {
			return nil, fmt.Errorf("source %q requires a non-empty credential", sourceName)
		}
		bootstrap[sourceName] = string(credential)
	}
	return bootstrap, nil
}

func interviewConfiguration(configuration config.Config, noSchedule bool, writer io.Writer) (config.Config, error) {
	reader := bufio.NewReader(os.Stdin)
	for _, sourceName := range configuration.SourcePriority {
		jobNames := jobsForSource(configuration, sourceName)
		for _, jobName := range jobNames {
			job := configuration.Jobs[jobName]
			enabled, err := askYesNo(reader, writer, "Enable "+sourceName+"?", job.Enabled)
			if err != nil {
				return config.Config{}, err
			}
			job.Enabled = enabled
			if enabled {
				job.Policy.IncludeFree, err = askYesNo(reader, writer, "Include ordinary free models for "+sourceName+"?", job.Policy.IncludeFree)
				if err != nil {
					return config.Config{}, err
				}
				job.Policy.IncludeDiscounted, err = askYesNo(reader, writer, "Include discounted paid models for "+sourceName+"?", job.Policy.IncludeDiscounted)
				if err != nil {
					return config.Config{}, err
				}
				if job.Policy.IncludeDiscounted {
					ceilings, err := askCeilings(reader, writer, job.Policy.PriceCeilings)
					if err != nil {
						return config.Config{}, err
					}
					job.Policy.PriceCeilings = ceilings
				}
				job.Limit, err = askInteger(reader, writer, "Maximum selected models for "+sourceName, job.Limit)
				if err != nil {
					return config.Config{}, err
				}
				job.MinContext, err = askInt64(reader, writer, "Minimum context tokens for "+sourceName, job.MinContext)
				if err != nil {
					return config.Config{}, err
				}
			}
			configuration.Jobs[jobName] = job
		}
	}
	if len(configuration.SourcePriority) > 1 {
		priorityText, err := askText(reader, writer, "Source priority, highest first and comma separated", strings.Join(configuration.SourcePriority, ","))
		if err != nil {
			return config.Config{}, err
		}
		priority, err := parsePriority(priorityText, configuration.Sources)
		if err != nil {
			return config.Config{}, err
		}
		configuration.SourcePriority = priority
	}
	if !noSchedule {
		enabled, err := askYesNo(reader, writer, "Enable daily native scheduling when available?", configuration.Schedule.Enabled)
		if err != nil {
			return config.Config{}, err
		}
		configuration.Schedule.Enabled = enabled
		if enabled {
			configuration.Schedule.LocalTime, err = askText(reader, writer, "Daily local schedule time (HH:MM)", configuration.Schedule.LocalTime)
			if err != nil {
				return config.Config{}, err
			}
		}
	}
	return configuration, nil
}

func jobsForSource(configuration config.Config, sourceName string) []string {
	var result []string
	for jobName, job := range configuration.Jobs {
		if job.Source == sourceName {
			result = append(result, jobName)
		}
	}
	sort.Strings(result)
	return result
}

func parsePriority(value string, sources map[string]config.Source) ([]string, error) {
	parts := strings.Split(value, ",")
	if len(parts) != len(sources) {
		return nil, fmt.Errorf("source priority must list every configured source exactly once")
	}
	seen := make(map[string]bool, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if _, exists := sources[name]; !exists || seen[name] {
			return nil, fmt.Errorf("source priority contains unknown or duplicate source %q", name)
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, nil
}

func askText(reader *bufio.Reader, writer io.Writer, question, defaultValue string) (string, error) {
	fmt.Fprintf(writer, "%s [%s] ", question, defaultValue)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return defaultValue, nil
	}
	return answer, nil
}

func askYesNo(reader *bufio.Reader, writer io.Writer, question string, defaultValue bool) (bool, error) {
	defaultText := "y/N"
	if defaultValue {
		defaultText = "Y/n"
	}
	for {
		fmt.Fprintf(writer, "%s [%s] ", question, defaultText)
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		if errors.Is(err, io.EOF) {
			return false, io.ErrUnexpectedEOF
		}
	}
}

func askInteger(reader *bufio.Reader, writer io.Writer, question string, defaultValue int) (int, error) {
	value, err := askInt64(reader, writer, question, int64(defaultValue))
	return int(value), err
}

func askInt64(reader *bufio.Reader, writer io.Writer, question string, defaultValue int64) (int64, error) {
	for {
		fmt.Fprintf(writer, "%s [%d] ", question, defaultValue)
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return defaultValue, nil
		}
		value, parseErr := strconv.ParseInt(answer, 10, 64)
		if parseErr == nil && value > 0 {
			return value, nil
		}
		if errors.Is(err, io.EOF) {
			return 0, io.ErrUnexpectedEOF
		}
	}
}

func askCeilings(reader *bufio.Reader, writer io.Writer, defaults map[string]string) (map[string]string, error) {
	defaultText := ""
	if len(defaults) > 0 {
		keys := make([]string, 0, len(defaults))
		for key := range defaults {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+"="+defaults[key])
		}
		defaultText = strings.Join(parts, ",")
	}
	for {
		fmt.Fprintf(writer, "Price ceilings as dimension|unit|currency=value, comma separated [%s] ", defaultText)
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			if len(defaults) > 0 {
				return defaults, nil
			}
		} else {
			result := make(map[string]string)
			valid := true
			for _, item := range strings.Split(answer, ",") {
				key, value, found := strings.Cut(strings.TrimSpace(item), "=")
				if !found || key == "" || value == "" {
					valid = false
					break
				}
				result[key] = value
			}
			if valid {
				return result, nil
			}
		}
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
	}
}

func parseSetupOptions(arguments []string) (setupOptions, error) {
	var result setupOptions
	for _, argument := range arguments {
		switch argument {
		case "--yes", "-y":
			result.yes = true
		case "--json":
			result.json = true
		case "--no-schedule":
			result.noSchedule = true
		case "--help", "-h":
			result.help = true
		default:
			return setupOptions{}, fmt.Errorf("unknown setup option %q", argument)
		}
	}
	return result, nil
}

func printSetupUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: llmloot setup [--yes] [--no-schedule] [--json]")
}
