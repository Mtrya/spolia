package cli

import (
	"testing"

	"github.com/Mtrya/llmloot/internal/config"
)

func TestSyncOptionsAcceptJobBeforeOrAfterFlags(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{"job-name", "--dry-run", "--json"},
		{"--dry-run", "job-name", "--json"},
	} {
		options, err := parseSyncOptions(arguments)
		if err != nil {
			t.Fatal(err)
		}
		if options.job != "job-name" || !options.dryRun || !options.json {
			t.Fatalf("options = %#v", options)
		}
	}
}

func TestLifecycleOptionsRequireExplicitFlags(t *testing.T) {
	t.Parallel()
	setup, err := parseSetupOptions([]string{"--yes", "--no-schedule", "--json"})
	if err != nil || !setup.yes || !setup.noSchedule || !setup.json {
		t.Fatalf("setup options = %#v, err = %v", setup, err)
	}
	uninstall, err := parseUninstallOptions([]string{"--dry-run", "--yes", "--json"})
	if err != nil || !uninstall.dryRun || !uninstall.yes || !uninstall.json {
		t.Fatalf("uninstall options = %#v, err = %v", uninstall, err)
	}
	if _, err := parseDoctorOptions([]string{"--repair"}); err == nil {
		t.Fatal("doctor accepted an unsupported repair mode")
	}
}

func TestSourcePriorityRequiresAnExactPermutation(t *testing.T) {
	t.Parallel()
	sources := config.Default().Sources
	priority, err := parsePriority("zenmux, openrouter", sources)
	if err != nil || len(priority) != 2 || priority[0] != "zenmux" {
		t.Fatalf("priority = %#v, err = %v", priority, err)
	}
	if _, err := parsePriority("openrouter,openrouter", sources); err == nil {
		t.Fatal("duplicate source priority was accepted")
	}
}

func TestSyncOptionsRejectUnknownAndMultipleJobs(t *testing.T) {
	t.Parallel()
	if _, err := parseSyncOptions([]string{"--unknown"}); err == nil {
		t.Fatal("unknown option was accepted")
	}
	if _, err := parseSyncOptions([]string{"one", "two"}); err == nil {
		t.Fatal("multiple jobs were accepted")
	}
}
