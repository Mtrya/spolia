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

	"github.com/Mtrya/spolia/internal/app"
	"github.com/Mtrya/spolia/internal/config"
	"github.com/Mtrya/spolia/internal/output"
	"github.com/Mtrya/spolia/internal/schedule"
	"github.com/Mtrya/spolia/internal/state"
	"github.com/Mtrya/spolia/internal/target/kimicode"
	"golang.org/x/term"
)

type setupOptions struct {
	yes        bool
	json       bool
	noSchedule bool
	advanced   bool
	help       bool
}

func runSetup(ctx context.Context, arguments []string, stdout, stderr io.Writer) (exitCode int) {
	options, err := parseSetupOptions(arguments)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printSetupUsage(stderr)
		return exitUsage
	}
	if options.help {
		printSetupUsage(stdout)
		return exitOK
	}
	if options.json && !options.yes {
		fmt.Fprintln(stderr, "--json requires --yes")
		return exitUsage
	}
	configPath, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	configuration, notConfigured, err := loadConfiguration(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if notConfigured {
		configuration = config.Default()
	}
	if options.noSchedule {
		configuration.Schedule.Enabled = false
	}

	stage(stderr, options.json, "Checking Kimi Code...")
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
	specs, err := allProviderSpecs(configuration)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	available, incompatible := inspectSourceCredentials(configuration, document, specs)

	prompted := map[string]string{}
	if !options.yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(stderr, "interactive setup requires a terminal; use --yes for the current configuration or stealth-only defaults")
			return exitFailure
		}
		configuration, prompted, err = interviewConfiguration(configuration, options.noSchedule, options.advanced, available, incompatible, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
	} else {
		// Non-interactive setup narrows to the sources that can actually run
		// instead of demanding a credential for every default source.
		for jobName, job := range configuration.Jobs {
			if !job.Enabled {
				continue
			}
			if reason, bad := incompatible[job.Source]; bad {
				job.Enabled = false
				configuration.Jobs[jobName] = job
				fmt.Fprintf(stderr, "skipping %s: Kimi Code provider is incompatible (%s)\n", job.Source, reason)
			} else if !available[job.Source] {
				job.Enabled = false
				configuration.Jobs[jobName] = job
				fmt.Fprintf(stderr, "skipping %s: no credential available; set %s or add the provider in Kimi Code to enable it\n", job.Source, configuration.Sources[job.Source].CredentialEnv)
			}
		}
	}
	if err := configuration.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	jobs, err := configuration.EnabledJobs("")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	for _, job := range jobs {
		if reason, bad := incompatible[job.Source]; bad {
			fmt.Fprintf(stderr, "Kimi Code provider %q is incompatible: %s\n", specs[job.Source].ID, reason)
			return exitFailure
		}
	}
	if len(jobs) == 0 {
		fmt.Fprintf(stderr, "setup needs at least one enabled source with a credential; set %s, or add a provider in Kimi Code\n", credentialEnvList(configuration))
		return exitFailure
	}
	targetName, err := onlyKimiTarget(configuration)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	bootstrap := bootstrapCredentials(configuration, document, specs, prompted)

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
	currentState, _, err := state.Read(config.StatePath(configPath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	// Persist the interviewed answers before any network operation so a later
	// failure keeps them as the defaults for the next run.
	if err := config.Save(configPath, configuration); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}

	providers, err := providersForJobs(configuration, jobs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	credentials := kimicode.Credentials{Document: document, Providers: providers, Bootstrap: bootstrap}
	stage(stderr, options.json, "Contacting providers (this can take up to a minute)...")
	result := application(credentials).Plan(ctx, configuration, currentState, "", false)
	result.Operation = "setup"
	requirements := providerRequirements(ctx, credentials, providers)
	targetPlan := kimicode.PlanSync(document, currentState.Targets[targetName], requirements, selections(result, providers))
	attachTargetPlan(&result, targetName, configuration.Targets[targetName].Adapter, targetPlan)
	if len(targetPlan.Conflicts) == 0 && result.Outcome != "failure" {
		stage(stderr, options.json, "Validating the Kimi Code configuration...")
		if err := targetPlan.Validate(ctx, installation); err != nil {
			result.Outcome = "failure"
			result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(targetName, err))
		}
	}
	if result.Outcome == "failure" || len(targetPlan.Conflicts) > 0 {
		if err := writeSetupResult(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
		}
		return exitFailure
	}
	if !options.yes {
		if err := output.Plan(stdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
		fmt.Fprintln(stdout, schedulePreview(configuration))
		confirmed, err := askYesNo(bufio.NewReader(os.Stdin), stdout, "Apply these changes?", false)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
		if !confirmed {
			fmt.Fprintln(stderr, "setup cancelled; your answers were saved as the new defaults")
			return exitFailure
		}
	}
	if err := targetPlan.Apply(ctx, installation); err != nil {
		result.Outcome = "failure"
		result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(targetName, err))
		if options.yes {
			_ = writeSetupResult(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, compactError(err.Error()))
		return exitFailure
	}
	updateState(&currentState, result, targetName, targetPlan.Ownership)
	if err := satisfyBoundaryAfterFullSuccess(&currentState, configuration, result.Outcome, "", time.Now()); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if err := state.Save(config.StatePath(configPath), currentState); err != nil {
		result.Outcome = "failure"
		result.TargetPlans[0].Conflicts = append(result.TargetPlans[0].Conflicts, appTargetError(targetName, err))
		if options.yes {
			_ = writeSetupResult(stdout, result, options.json)
		}
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if configuration.Schedule.Enabled {
		stage(stderr, options.json, "Installing the daily schedule...")
	}
	inspection, schedulerErr := reconcileConfiguredScheduler(ctx, config.StatePath(configPath), configuration, &currentState)
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
	if schedulerErr != nil {
		result.Schedule.Status = "error"
		result.Schedule.Error = compactError(schedulerErr.Error())
		if !configuration.Schedule.Enabled {
			// Honoring an explicit scheduling opt-out is not optional: the
			// old schedule would otherwise keep running while the saved
			// configuration claims it is gone.
			result.Outcome = "failure"
			if options.yes {
				_ = writeSetupResult(stdout, result, options.json)
			}
			fmt.Fprintln(stderr, compactError(schedulerErr.Error()))
			return exitFailure
		}
		// A scheduling install problem must not present the whole setup as
		// failed: the models, configuration, and state are already in place.
	}
	if options.yes {
		if err := writeSetupResult(stdout, result, options.json); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
	} else {
		if err := output.Summary(stdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
	}
	if result.Outcome == "success" {
		return exitOK
	}
	return exitFailure
}

func writeSetupResult(writer io.Writer, result app.SyncResult, asJSON bool) error {
	if asJSON {
		return output.JSON(writer, result)
	}
	return output.Human(writer, result)
}

// stage reports a setup step on stderr so a slow network operation never
// looks like a frozen command. JSON mode stays silent to keep pipes clean.
func stage(stderr io.Writer, quiet bool, message string) {
	if !quiet {
		fmt.Fprintln(stderr, message)
	}
}

func schedulePreview(configuration config.Config) string {
	if !configuration.Schedule.Enabled {
		return "daily check: disabled"
	}
	if _, err := currentScheduleDefinition(configuration.Schedule.LocalTime); err != nil {
		return "daily check: unavailable (" + compactError(err.Error()) + ")"
	}
	return fmt.Sprintf("daily check: install a %s schedule at %s local time", schedule.Current().Kind(), configuration.Schedule.LocalTime)
}

// allProviderSpecs maps every configured source to its Kimi Code provider,
// whether or not its job is currently enabled.
func allProviderSpecs(configuration config.Config) (map[string]kimicode.ProviderSpec, error) {
	specs := make(map[string]kimicode.ProviderSpec, len(configuration.Sources))
	for name, source := range configuration.Sources {
		spec, err := kimicode.ProviderForSource(source.Adapter)
		if err != nil {
			return nil, err
		}
		specs[name] = spec
	}
	return specs, nil
}

// inspectSourceCredentials reports which sources can authenticate right now,
// either through an existing Kimi Code provider credential or through their
// environment variable, and separately notes incompatible providers.
// Incompatibility is not fatal here: it only matters for sources that remain
// enabled after the interview or the --yes narrowing.
func inspectSourceCredentials(configuration config.Config, document kimicode.Document, specs map[string]kimicode.ProviderSpec) (map[string]bool, map[string]string) {
	available := make(map[string]bool, len(specs))
	incompatible := make(map[string]string)
	for name, spec := range specs {
		inspection := document.Provider(spec)
		if inspection.Exists && !inspection.Compatible {
			incompatible[name] = inspection.Reason
			continue
		}
		available[name] = inspection.CredentialExists || os.Getenv(configuration.Sources[name].CredentialEnv) != ""
	}
	return available, incompatible
}

// bootstrapCredentials collects the credentials discovery needs before the
// provider entries exist in Kimi Code: keys the user just typed, then
// environment credentials for sources Kimi Code does not already know.
func bootstrapCredentials(configuration config.Config, document kimicode.Document, specs map[string]kimicode.ProviderSpec, prompted map[string]string) map[string]string {
	bootstrap := make(map[string]string)
	for name, spec := range specs {
		if credential, ok := prompted[name]; ok {
			bootstrap[name] = credential
			continue
		}
		if document.Provider(spec).CredentialExists {
			continue
		}
		if credential := os.Getenv(configuration.Sources[name].CredentialEnv); credential != "" {
			bootstrap[name] = credential
		}
	}
	return bootstrap
}

func credentialEnvList(configuration config.Config) string {
	names := make([]string, 0, len(configuration.Sources))
	for _, source := range configuration.Sources {
		names = append(names, source.CredentialEnv)
	}
	sort.Strings(names)
	return strings.Join(names, " or ")
}

func interviewConfiguration(configuration config.Config, noSchedule, advanced bool, available map[string]bool, incompatible map[string]string, writer io.Writer) (config.Config, map[string]string, error) {
	reader := bufio.NewReader(os.Stdin)
	prompted := make(map[string]string)
	for _, sourceName := range configuration.SourcePriority {
		jobNames := jobsForSource(configuration, sourceName)
		for _, jobName := range jobNames {
			job := configuration.Jobs[jobName]
			_, blocked := incompatible[sourceName]
			enabled, err := askYesNo(reader, writer, "Enable "+sourceName+"?", job.Enabled && available[sourceName] && !blocked)
			if err != nil {
				return config.Config{}, nil, err
			}
			job.Enabled = enabled
			if enabled {
				if !available[sourceName] {
					credential, err := askCredential(writer, sourceName)
					if err != nil {
						return config.Config{}, nil, err
					}
					prompted[sourceName] = credential
				}
				job.Policy.IncludeFree, err = askYesNo(reader, writer, "Also include ordinary free models for "+sourceName+"?", job.Policy.IncludeFree)
				if err != nil {
					return config.Config{}, nil, err
				}
				if advanced {
					job.Limit, err = askInteger(reader, writer, "Maximum selected models for "+sourceName, job.Limit)
					if err != nil {
						return config.Config{}, nil, err
					}
					job.MinContext, err = askInt64(reader, writer, "Minimum context tokens for "+sourceName, job.MinContext)
					if err != nil {
						return config.Config{}, nil, err
					}
				}
			}
			configuration.Jobs[jobName] = job
		}
	}
	if advanced && len(configuration.SourcePriority) > 1 {
		priorityText, err := askText(reader, writer, "Source priority, highest first and comma separated", strings.Join(configuration.SourcePriority, ","))
		if err != nil {
			return config.Config{}, nil, err
		}
		priority, err := parsePriority(priorityText, configuration.Sources)
		if err != nil {
			return config.Config{}, nil, err
		}
		configuration.SourcePriority = priority
	}
	if !noSchedule {
		enabled, err := askYesNo(reader, writer, "Enable daily native scheduling when available?", configuration.Schedule.Enabled)
		if err != nil {
			return config.Config{}, nil, err
		}
		configuration.Schedule.Enabled = enabled
		if enabled {
			configuration.Schedule.LocalTime, err = askText(reader, writer, "Daily local schedule time (HH:MM)", configuration.Schedule.LocalTime)
			if err != nil {
				return config.Config{}, nil, err
			}
		}
	}
	return configuration, prompted, nil
}

func askCredential(writer io.Writer, sourceName string) (string, error) {
	fmt.Fprintf(writer, "Enter the API key for %s: ", sourceName)
	credential, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(writer)
	if err != nil {
		return "", fmt.Errorf("read hidden credential for source %q: %w", sourceName, err)
	}
	if len(credential) == 0 {
		return "", fmt.Errorf("source %q requires a non-empty credential", sourceName)
	}
	return string(credential), nil
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
		fmt.Fprintln(writer, `please answer "y" or "n"`)
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
		fmt.Fprintln(writer, "enter a positive number")
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
		case "--advanced":
			result.advanced = true
		case "--help", "-h":
			result.help = true
		default:
			return setupOptions{}, fmt.Errorf("unknown setup option %q", argument)
		}
	}
	return result, nil
}

func printSetupUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: spolia setup [--yes] [--no-schedule] [--advanced] [--json]

Guides you through enabling providers and adds the selected models to Kimi Code.

  --yes          accept the current configuration without questions; sources
                 without a credential are skipped automatically
  --no-schedule  do not install the daily background check
  --advanced     also ask about model limits, minimum context, and source priority
  --json         print the result as JSON (requires --yes)

Example: export OPENROUTER_API_KEY=... && spolia setup`)
}
