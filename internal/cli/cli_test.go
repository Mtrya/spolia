package cli

import "testing"

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

func TestSyncOptionsRejectUnknownAndMultipleJobs(t *testing.T) {
	t.Parallel()
	if _, err := parseSyncOptions([]string{"--unknown"}); err == nil {
		t.Fatal("unknown option was accepted")
	}
	if _, err := parseSyncOptions([]string{"one", "two"}); err == nil {
		t.Fatal("multiple jobs were accepted")
	}
}
