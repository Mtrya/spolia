package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Mtrya/spolia/internal/config"
	"github.com/Mtrya/spolia/internal/schedule"
	"github.com/Mtrya/spolia/internal/state"
	"github.com/Mtrya/spolia/internal/target/kimicode"
)

type doctorResult struct {
	SchemaVersion int           `json:"schema_version"`
	Operation     string        `json:"operation"`
	Outcome       string        `json:"outcome"`
	Status        *doctorStatus `json:"status,omitempty"`
	Checks        []doctorCheck `json:"checks"`
}

// doctorStatus is the at-a-glance answer to "what is spolia doing right
// now": which models it manages, how the last runs went, and when the next
// scheduled check happens.
type doctorStatus struct {
	Models    []doctorModelStatus    `json:"models,omitempty"`
	Jobs      []doctorJobStatus      `json:"jobs,omitempty"`
	Scheduler *doctorSchedulerStatus `json:"scheduler,omitempty"`
}

type doctorModelStatus struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Job    string `json:"job"`
}

type doctorJobStatus struct {
	Name        string     `json:"name"`
	Outcome     string     `json:"outcome"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type doctorSchedulerStatus struct {
	Enabled   bool       `json:"enabled"`
	Kind      string     `json:"kind,omitempty"`
	LocalTime string     `json:"local_time,omitempty"`
	NextCheck *time.Time `json:"next_check,omitempty"`
}

type doctorCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type doctorOptions struct {
	json bool
	help bool
}

func runDoctor(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	options, err := parseDoctorOptions(arguments)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printDoctorUsage(stderr)
		return exitUsage
	}
	if options.help {
		printDoctorUsage(stdout)
		return exitOK
	}
	result := doctorResult{SchemaVersion: 1, Operation: "doctor", Outcome: "healthy", Checks: []doctorCheck{}}
	executable, executableErr := os.Executable()
	if executableErr != nil {
		result.addError("spolia_executable", executableErr.Error(), "Run spolia from a stable executable path.")
	} else {
		result.addOK("spolia_executable", executable)
	}
	configPath, pathErr := config.Path()
	if pathErr != nil {
		result.addError("spolia_config", pathErr.Error(), "Fix the user configuration directory environment.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	configuration, notConfigured, configErr := loadConfiguration(configPath)
	if notConfigured {
		result.Outcome = "not_configured"
		result.Checks = append(result.Checks, doctorCheck{Name: "spolia_config", Status: "ok", Detail: "spolia is not set up yet", Remediation: "Run spolia setup."})
		reportKimiEnvironment(ctx, &result)
		return writeDoctor(stdout, stderr, options.json, result)
	}
	if configErr != nil {
		result.addError("spolia_config", configErr.Error(), "Fix or remove "+configPath+", or run spolia setup to recreate it.")
	} else {
		result.addOK("spolia_config", "configuration is valid")
	}
	currentState, stateExists, stateErr := state.Read(config.StatePath(configPath))
	if stateErr != nil {
		result.addError("ownership_state", stateErr.Error(), "Restore the state file or remove owned target entries manually before running setup again.")
	} else if !stateExists {
		result.addError("ownership_state", "ownership state is missing", "Run spolia setup before sync or target cleanup.")
	} else {
		result.addOK("ownership_state", "ownership state is valid")
	}
	lockPath := config.LockPath(configPath)
	if _, lockErr := os.Stat(lockPath); lockErr == nil {
		result.Checks = append(result.Checks, doctorCheck{Name: "process_lock", Status: "warning", Detail: "a lock file exists at " + lockPath, Remediation: "If no spolia process is running, remove the stale lock file."})
	} else if errors.Is(lockErr, os.ErrNotExist) {
		result.addOK("process_lock", "no process lock is held")
	} else {
		result.addError("process_lock", lockErr.Error(), "Check the spolia home directory permissions.")
	}
	installation, installationErr := kimicode.Discover(ctx)
	if installationErr != nil {
		result.addError("kimi_code", installationErr.Error(), "Install a supported Kimi Code release and ensure kimi is in PATH.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	result.addOK("kimi_code", fmt.Sprintf("version %s at %s", installation.Version, installation.Binary))
	document, documentErr := kimicode.Load(installation.ConfigPath)
	if documentErr != nil {
		result.addError("kimi_config", documentErr.Error(), "Correct the Kimi Code config before running spolia setup or sync.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	if !document.Exists() {
		result.addError("kimi_config", "Kimi Code config does not exist", "Run spolia setup to create provider and model entries.")
	} else if err := installation.Validate(ctx, installation.ConfigPath); err != nil {
		result.addError("kimi_config", err.Error(), "Run kimi doctor config and correct the reported Kimi Code configuration error.")
	} else {
		result.addOK("kimi_config", "Kimi Code accepts the current configuration")
	}
	if configErr != nil {
		return writeDoctor(stdout, stderr, options.json, result)
	}
	jobs, err := configuration.EnabledJobs("")
	if err != nil {
		result.addError("enabled_jobs", err.Error(), "Correct the spolia job configuration.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	providers, err := providersForJobs(configuration, jobs)
	if err != nil {
		result.addError("providers", err.Error(), "Use a source adapter supported by the configured target.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	sourceNames := make([]string, 0, len(providers))
	for sourceName := range providers {
		sourceNames = append(sourceNames, sourceName)
	}
	sort.Strings(sourceNames)
	for _, sourceName := range sourceNames {
		inspection := document.Provider(providers[sourceName])
		switch {
		case !inspection.Exists:
			result.addError("provider:"+sourceName, "provider is missing", "Run spolia setup to create it.")
		case !inspection.Compatible:
			result.addError("provider:"+sourceName, inspection.Reason, "Rename the conflicting provider or correct it before setup.")
		case !inspection.CredentialExists:
			result.addError("provider:"+sourceName, "provider credential is missing", "Run spolia setup with the source credential available.")
		default:
			result.addOK("provider:"+sourceName, "provider is compatible and credentialed")
		}
	}
	credentials := kimicode.Credentials{Document: document, Providers: providers}
	discovery := application(credentials).Plan(ctx, configuration, currentState, "", true)
	for _, job := range discovery.Jobs {
		if job.Outcome == "error" {
			result.addError("catalog:"+job.Source, compactError(job.Error), "Check the Kimi Code provider credential, network access, and source catalog availability.")
		} else {
			result.addOK("catalog:"+job.Source, job.Outcome)
		}
	}
	if stateErr == nil && stateExists {
		targetName, targetErr := onlyKimiTarget(configuration)
		if targetErr != nil {
			result.addError("target", targetErr.Error(), "Configure one supported Kimi Code target.")
		} else {
			reported := make(map[string]bool)
			for _, conflict := range kimicode.InspectOwnership(document, currentState.Targets[targetName]) {
				name := "managed:" + conflict.Kind + ":" + conflict.ID
				reported[name] = true
				result.addError(name, conflict.Reason, "Resolve the conflicting target edit, then rerun doctor.")
			}
			requirements := providerRequirements(ctx, credentials, providers)
			plan := kimicode.PlanSync(document, currentState.Targets[targetName], requirements, selections(discovery, providers))
			if len(plan.Conflicts) > 0 {
				for _, conflict := range plan.Conflicts {
					name := "managed:" + conflict.Kind + ":" + conflict.ID
					if !reported[name] {
						result.addError(name, conflict.Reason, "Resolve the conflicting target edit, then rerun doctor.")
					}
				}
			} else if err := plan.Validate(ctx, installation); err != nil {
				result.addError("planned_target", err.Error(), "Correct the Kimi Code configuration or selected model fields.")
			} else {
				result.addOK("planned_target", "the current reconciliation plan is valid")
			}
			for _, protected := range plan.Protected {
				result.Checks = append(result.Checks, doctorCheck{Name: "protected:model:" + protected.ID, Status: "warning", Detail: "referenced by " + strings.Join(protected.References, ", "), Remediation: "Change the Kimi Code reference before expecting spolia to remove this alias."})
			}
		}
	}
	checkScheduler(ctx, configuration, currentState, stateErr == nil && stateExists, &result)
	if stateErr == nil && stateExists {
		result.Status = buildDoctorStatus(configuration, currentState)
		jobNames := make([]string, 0, len(currentState.Jobs))
		for jobName := range currentState.Jobs {
			jobNames = append(jobNames, jobName)
		}
		sort.Strings(jobNames)
		for _, jobName := range jobNames {
			jobState := currentState.Jobs[jobName]
			status := "ok"
			if jobState.Outcome == "error" {
				status = "warning"
			}
			result.Checks = append(result.Checks, doctorCheck{Name: "last_run:" + jobName, Status: status, Detail: jobState.Outcome})
		}
	}
	return writeDoctor(stdout, stderr, options.json, result)
}

func checkScheduler(ctx context.Context, configuration config.Config, currentState state.State, stateHealthy bool, result *doctorResult) {
	if !stateHealthy {
		return
	}
	if currentState.Scheduler == nil {
		if configuration.Schedule.Enabled {
			result.addError("scheduler", "scheduling is enabled but scheduler ownership state is missing", "Run spolia setup to install the native per-user schedule.")
		} else {
			result.addOK("scheduler", "scheduling is disabled")
		}
		return
	}
	_, inspection, err := inspectStoredScheduler(ctx, *currentState.Scheduler)
	if err != nil {
		result.addError("scheduler", err.Error(), "Correct the scheduler state or rerun spolia setup on this platform.")
		return
	}
	if !configuration.Schedule.Enabled {
		if inspection.Installed {
			result.addError("scheduler", "scheduling is disabled but the owned native schedule remains installed", "Run spolia setup --no-schedule or spolia uninstall.")
		} else {
			result.addError("scheduler", "scheduling is disabled but stale scheduler ownership state remains", "Run spolia setup --no-schedule to clear it.")
		}
		return
	}
	desired, err := currentScheduleDefinition(configuration.Schedule.LocalTime)
	if err != nil {
		result.addError("scheduler", err.Error(), "Run spolia setup from a stable executable path.")
		return
	}
	switch {
	case currentState.Scheduler.Identifier != schedule.DefaultIdentifier:
		result.addError("scheduler", fmt.Sprintf("unexpected scheduler identifier %q", currentState.Scheduler.Identifier), "Run spolia setup to restore the one supported scheduler identifier.")
	case !inspection.Installed:
		result.addError("scheduler", "the owned native schedule is not installed", "Run spolia setup to install it.")
	case !inspection.Managed:
		result.addError("scheduler", "the native scheduler artifact is not owned by spolia", "Move the conflicting artifact, then rerun spolia setup.")
	case !inspection.Matches:
		result.addError("scheduler", inspection.Detail, "Restore the scheduler definition or rerun spolia setup after reviewing the edit.")
	case !inspection.Enabled:
		result.addError("scheduler", inspection.Detail, "Run spolia setup to enable the native schedule.")
	case currentState.Scheduler.ExecutablePath != desired.Executable || currentState.Scheduler.LocalTime != desired.LocalTime:
		result.addError("scheduler", "the installed schedule does not match the current executable path or configured time", "Run spolia setup to update the native schedule.")
	default:
		detail := fmt.Sprintf("%s %s at %s", inspection.Kind, inspection.Status, currentState.Scheduler.LocalTime)
		due, boundary, err := schedule.IsDue(time.Now().In(time.Local), currentState.Scheduler.LocalTime, currentState.LastSuccessfulScheduleBoundary)
		if err == nil {
			next := boundary
			if !due {
				next = boundary.AddDate(0, 0, 1)
			}
			detail += "; next check " + next.Local().Format("2006-01-02 15:04")
		}
		result.addOK("scheduler", detail)
	}
}

// reportKimiEnvironment adds the Kimi Code prerequisite checks that do not
// depend on a spolia configuration, so a first-run doctor still tells the
// user whether the required binary is present.
func reportKimiEnvironment(ctx context.Context, result *doctorResult) {
	installation, err := kimicode.Discover(ctx)
	if err != nil {
		result.addError("kimi_code", err.Error(), "Install a supported Kimi Code release and ensure kimi is in PATH.")
		return
	}
	result.addOK("kimi_code", fmt.Sprintf("version %s at %s", installation.Version, installation.Binary))
}

// buildDoctorStatus summarizes what spolia currently manages from the
// ownership state: the models present in Kimi Code, each job's last run,
// and the next scheduled check.
func buildDoctorStatus(configuration config.Config, currentState state.State) *doctorStatus {
	status := &doctorStatus{}
	for _, targetName := range sortedTargetNames(currentState) {
		for alias, owned := range currentState.Targets[targetName].Models {
			status.Models = append(status.Models, doctorModelStatus{ID: alias, Source: owned.Source, Job: owned.Job})
		}
	}
	sort.Slice(status.Models, func(left, right int) bool { return status.Models[left].ID < status.Models[right].ID })
	jobNames := make([]string, 0, len(currentState.Jobs))
	for jobName := range currentState.Jobs {
		jobNames = append(jobNames, jobName)
	}
	sort.Strings(jobNames)
	for _, jobName := range jobNames {
		jobState := currentState.Jobs[jobName]
		status.Jobs = append(status.Jobs, doctorJobStatus{Name: jobName, Outcome: jobState.Outcome, LastAttempt: jobState.LastAttempt, Error: jobState.Error})
	}
	localTime := configuration.Schedule.LocalTime
	if currentState.Scheduler != nil && currentState.Scheduler.LocalTime != "" {
		localTime = currentState.Scheduler.LocalTime
	}
	schedulerStatus := &doctorSchedulerStatus{Enabled: configuration.Schedule.Enabled, LocalTime: localTime}
	if currentState.Scheduler != nil {
		schedulerStatus.Kind = currentState.Scheduler.Kind
	}
	if schedulerStatus.Enabled {
		due, boundary, err := schedule.IsDue(time.Now().In(time.Local), localTime, currentState.LastSuccessfulScheduleBoundary)
		if err == nil {
			next := boundary
			if !due {
				next = boundary.AddDate(0, 0, 1)
			}
			schedulerStatus.NextCheck = &next
		}
	}
	status.Scheduler = schedulerStatus
	return status
}

func sortedTargetNames(currentState state.State) []string {
	names := make([]string, 0, len(currentState.Targets))
	for name := range currentState.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (result *doctorResult) addOK(name, detail string) {
	result.Checks = append(result.Checks, doctorCheck{Name: name, Status: "ok", Detail: detail})
}

func (result *doctorResult) addError(name, detail, remediation string) {
	result.Outcome = "unhealthy"
	result.Checks = append(result.Checks, doctorCheck{Name: name, Status: "error", Detail: compactError(detail), Remediation: remediation})
}

func writeDoctor(stdout, stderr io.Writer, asJSON bool, result doctorResult) int {
	var err error
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		err = encoder.Encode(result)
	} else {
		_, err = fmt.Fprintf(stdout, "doctor: %s\n", result.Outcome)
		if err == nil && result.Status != nil {
			err = writeDoctorStatus(stdout, result.Status)
		}
		for _, check := range result.Checks {
			if err != nil {
				break
			}
			_, err = fmt.Fprintf(stdout, "%s  %s", check.Status, check.Name)
			if err == nil && check.Detail != "" {
				_, err = fmt.Fprintf(stdout, ": %s", check.Detail)
			}
			if err == nil {
				_, err = fmt.Fprintln(stdout)
			}
			if err == nil && check.Remediation != "" {
				_, err = fmt.Fprintf(stdout, "  %s\n", check.Remediation)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if result.Outcome == "healthy" || result.Outcome == "not_configured" {
		return exitOK
	}
	return exitFailure
}

func writeDoctorStatus(stdout io.Writer, status *doctorStatus) error {
	if _, err := fmt.Fprintln(stdout, "status:"); err != nil {
		return err
	}
	for _, model := range status.Models {
		if _, err := fmt.Fprintf(stdout, "  model %s (try: kimi --model '%s')\n", model.ID, model.ID); err != nil {
			return err
		}
	}
	for _, job := range status.Jobs {
		outcome := job.Outcome
		if outcome == "" {
			outcome = "never run"
		}
		line := "  last " + job.Name + ": " + outcome
		if job.LastAttempt != nil {
			line += " at " + job.LastAttempt.Local().Format("2006-01-02 15:04")
		}
		if job.Error != "" {
			line += " (" + job.Error + ")"
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	if scheduler := status.Scheduler; scheduler != nil {
		line := ""
		switch {
		case !scheduler.Enabled:
			line = "  daily check: disabled"
		default:
			line = "  daily check: at " + scheduler.LocalTime
			if scheduler.Kind == "" {
				line += " (not installed)"
			}
			if scheduler.NextCheck != nil {
				line += ", next " + scheduler.NextCheck.Local().Format("2006-01-02 15:04")
			}
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func parseDoctorOptions(arguments []string) (doctorOptions, error) {
	var result doctorOptions
	for _, argument := range arguments {
		switch argument {
		case "--json":
			result.json = true
		case "--help", "-h":
			result.help = true
		default:
			return doctorOptions{}, fmt.Errorf("unknown doctor option %q", argument)
		}
	}
	return result, nil
}

func printDoctorUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: spolia doctor [--json]

Shows what spolia currently manages and checks that everything is working.
Read-only: doctor never changes any file.

  --json  print the result as JSON

Example: spolia doctor`)
}
