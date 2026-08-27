package output

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Mtrya/llmloot/internal/app"
)

func TestJSONCarriesVersionedMachineDecisions(t *testing.T) {
	t.Parallel()
	input := app.SyncResult{
		SchemaVersion: 1,
		Operation:     "sync",
		DryRun:        true,
		Outcome:       "success",
		Jobs:          []app.JobResult{},
		TargetPlans:   []app.TargetPlan{},
	}
	var buffer bytes.Buffer
	if err := JSON(&buffer, input); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		SchemaVersion int    `json:"schema_version"`
		Operation     string `json:"operation"`
		DryRun        bool   `json:"dry_run"`
		Outcome       string `json:"outcome"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 1 || decoded.Operation != "sync" || !decoded.DryRun || decoded.Outcome != "success" {
		t.Fatalf("decoded result = %#v", decoded)
	}
}
