package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/schedule"
	"github.com/Mtrya/llmloot/internal/state"
	"github.com/Mtrya/llmloot/internal/target/kimicode"
)

type doctorResult struct {
	SchemaVersion int           `json:"schema_version"`
	Operation     string        `json:"operation"`
	Outcome       string        `json:"outcome"`
	Checks        []doctorCheck `json:"checks"`
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
		return 2
	}
	if options.help {
		printDoctorUsage(stdout)
		return 0
	}
	result := doctorResult{SchemaVersion: 1, Operation: "doctor", Outcome: "healthy", Checks: []doctorCheck{}}
	executable, executableErr := os.Executable()
	if executableErr != nil {
		result.addError("llmloot_executable", executableErr.Error(), "Run llmloot from a stable executable path.")
	} else {
		result.addOK("llmloot_executable", executable)
	}
	configPath, pathErr := config.Path()
	if pathErr != nil {
		result.addError("llmloot_config", pathErr.Error(), "Fix the user configuration directory environment.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	configuration, configErr := config.Load(configPath)
	if configErr != nil {
		result.addError("llmloot_config", configErr.Error(), "Run llmloot setup after correcting or removing the invalid config.")
	} else {
		result.addOK("llmloot_config", "configuration is valid")
	}
	currentState, stateExists, stateErr := state.Read(config.StatePath(configPath))
	if stateErr != nil {
		result.addError("ownership_state", stateErr.Error(), "Restore the state file or remove owned target entries manually before running setup again.")
	} else if !stateExists {
		result.addError("ownership_state", "ownership state is missing", "Run llmloot setup before sync or target cleanup.")
	} else {
		result.addOK("ownership_state", "ownership state is valid")
	}
	installation, installationErr := kimicode.Discover(ctx)
	if installationErr != nil {
		result.addError("kimi_code", installationErr.Error(), "Install a supported Kimi Code release and ensure kimi is in PATH.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	result.addOK("kimi_code", fmt.Sprintf("version %s at %s", installation.Version, installation.Binary))
	document, documentErr := kimicode.Load(installation.ConfigPath)
	if documentErr != nil {
		result.addError("kimi_config", documentErr.Error(), "Correct the Kimi Code config before running llmloot setup or sync.")
		return writeDoctor(stdout, stderr, options.json, result)
	}
	if !document.Exists() {
		result.addError("kimi_config", "Kimi Code config does not exist", "Run llmloot setup to create provider and model entries.")
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
		result.addError("enabled_jobs", err.Error(), "Correct the llmloot job configuration.")
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
			result.addError("provider:"+sourceName, "provider is missing", "Run llmloot setup to create it.")
		case !inspection.Compatible:
			result.addError("provider:"+sourceName, inspection.Reason, "Rename the conflicting provider or correct it before setup.")
		case !inspection.CredentialExists:
			result.addError("provider:"+sourceName, "provider credential is missing", "Run llmloot setup with the source credential available.")
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
				result.Checks = append(result.Checks, doctorCheck{Name: "protected:model:" + protected.ID, Status: "warning", Detail: "referenced by " + strings.Join(protected.References, ", "), Remediation: "Change the Kimi Code reference before expecting llmloot to remove this alias."})
			}
		}
	}
	checkScheduler(ctx, configuration, currentState, stateErr == nil && stateExists, &result)
	if stateErr == nil && stateExists {
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
			result.addError("scheduler", "scheduling is enabled but scheduler ownership state is missing", "Run llmloot setup to install the native per-user schedule.")
		} else {
			result.addOK("scheduler", "scheduling is disabled")
		}
		return
	}
	_, inspection, err := inspectStoredScheduler(ctx, *currentState.Scheduler)
	if err != nil {
		result.addError("scheduler", err.Error(), "Correct the scheduler state or rerun llmloot setup on this platform.")
		return
	}
	if !configuration.Schedule.Enabled {
		if inspection.Installed {
			result.addError("scheduler", "scheduling is disabled but the owned native schedule remains installed", "Run llmloot setup --no-schedule or llmloot uninstall.")
		} else {
			result.addError("scheduler", "scheduling is disabled but stale scheduler ownership state remains", "Run llmloot setup --no-schedule to clear it.")
		}
		return
	}
	desired, err := currentScheduleDefinition(configuration.Schedule.LocalTime)
	if err != nil {
		result.addError("scheduler", err.Error(), "Run llmloot setup from a stable executable path.")
		return
	}
	switch {
	case currentState.Scheduler.Identifier != schedule.DefaultIdentifier:
		result.addError("scheduler", fmt.Sprintf("unexpected scheduler identifier %q", currentState.Scheduler.Identifier), "Run llmloot setup to restore the one supported scheduler identifier.")
	case !inspection.Installed:
		result.addError("scheduler", "the owned native schedule is not installed", "Run llmloot setup to install it.")
	case !inspection.Managed:
		result.addError("scheduler", "the native scheduler artifact is not owned by llmloot", "Move the conflicting artifact, then rerun llmloot setup.")
	case !inspection.Matches:
		result.addError("scheduler", inspection.Detail, "Restore the scheduler definition or rerun llmloot setup after reviewing the edit.")
	case !inspection.Enabled:
		result.addError("scheduler", inspection.Detail, "Run llmloot setup to enable the native schedule.")
	case currentState.Scheduler.ExecutablePath != desired.Executable || currentState.Scheduler.LocalTime != desired.LocalTime:
		result.addError("scheduler", "the installed schedule does not match the current executable path or configured time", "Run llmloot setup to update the native schedule.")
	default:
		result.addOK("scheduler", fmt.Sprintf("%s %s at %s", inspection.Kind, inspection.Status, currentState.Scheduler.LocalTime))
	}
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
		return 1
	}
	if result.Outcome == "healthy" {
		return 0
	}
	return 1
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
	fmt.Fprintln(writer, "usage: llmloot doctor [--json]")
}
