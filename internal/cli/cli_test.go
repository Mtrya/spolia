package cli

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mtrya/spolia/internal/config"
	"github.com/Mtrya/spolia/internal/state"
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

func TestIfDueRejectsSingleJobAtTheCommandBoundary(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	if exitCode := Run(context.Background(), []string{"sync", "openrouter-kimi-code", "--if-due"}, &stdout, &stderr); exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
}

func TestAskYesNoExplainsInvalidInput(t *testing.T) {
	t.Parallel()
	reader := bufio.NewReader(strings.NewReader("maybe\nyes\n"))
	var output strings.Builder
	value, err := askYesNo(reader, &output, "Continue?", false)
	if err != nil || !value {
		t.Fatalf("value = %v, err = %v", value, err)
	}
	if !strings.Contains(output.String(), `please answer "y" or "n"`) {
		t.Fatalf("invalid input was not explained: %q", output.String())
	}
}

func TestHelpOrganizesCommandsByUserGoal(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	if exitCode := Run(context.Background(), []string{"help"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d", exitCode)
	}
	text := stdout.String()
	for _, command := range []string{"setup", "sync", "doctor", "uninstall"} {
		if !strings.Contains(text, "spolia "+command) {
			t.Fatalf("help does not mention %q:\n%s", command, text)
		}
	}
	for _, subcommand := range []string{"setup", "sync", "doctor", "uninstall"} {
		var out strings.Builder
		if exitCode := Run(context.Background(), []string{subcommand, "--help"}, &out, &strings.Builder{}); exitCode != 0 {
			t.Fatalf("%s --help exit code = %d", subcommand, exitCode)
		}
		if !strings.Contains(out.String(), "Example:") {
			t.Fatalf("%s --help has no example:\n%s", subcommand, out.String())
		}
	}
}

func TestLastSuccessfulCheckUsesJobTimestamps(t *testing.T) {
	t.Parallel()
	if lastSuccessfulCheck(state.New()) != nil {
		t.Fatal("empty state reported a last check")
	}
	early := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 8, 29, 17, 30, 0, 0, time.UTC)
	current := state.New()
	current.RecordJob("first", "selected", nil, "", early)
	current.RecordJob("second", "selected", nil, "", late)
	got := lastSuccessfulCheck(current)
	if got == nil || !got.Equal(late) {
		t.Fatalf("lastSuccessfulCheck = %v, want %v", got, late)
	}
}

func TestCatalogEndpointOverrideIsLimitedToLoopback(t *testing.T) {
	t.Setenv("SPOLIA_TEST_ENDPOINT", "http://127.0.0.1:8080/models")
	if endpoint := loopbackTestEndpoint("SPOLIA_TEST_ENDPOINT"); endpoint == "" {
		t.Fatal("loopback fixture endpoint was rejected")
	}
	t.Setenv("SPOLIA_TEST_ENDPOINT", "https://example.com/models")
	if endpoint := loopbackTestEndpoint("SPOLIA_TEST_ENDPOINT"); endpoint != "" {
		t.Fatalf("non-loopback fixture endpoint was accepted: %q", endpoint)
	}
}
