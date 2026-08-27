package config

import (
	"strings"
	"testing"
)

const validConfig = `
schema_version = 1
source_priority = ["openrouter", "zenmux"]

[sources.openrouter]
adapter = "openrouter"
credential_env = "OPENROUTER_API_KEY"

[sources.zenmux]
adapter = "zenmux"
credential_env = "ZENMUX_API_KEY"

[targets.kimi-code]
adapter = "kimi-code"

[jobs.openrouter-kimi-code]
enabled = true
source = "openrouter"
target = "kimi-code"

[jobs.openrouter-kimi-code.policy]
include_free = false
include_discounted = false

[jobs.zenmux-kimi-code]
enabled = true
source = "zenmux"
target = "kimi-code"
limit = 2
min_context = 200000

[jobs.zenmux-kimi-code.policy]
include_free = true
include_discounted = false
`

func TestDecodeAppliesJobDefaultsAndPriority(t *testing.T) {
	t.Parallel()
	configuration, err := Decode(strings.NewReader(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	job := configuration.Jobs["openrouter-kimi-code"]
	if job.Limit != 3 || job.MinContext != 131072 {
		t.Fatalf("defaults = limit %d, context %d", job.Limit, job.MinContext)
	}
	jobs, err := configuration.EnabledJobs("")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Source != "openrouter" || jobs[1].Source != "zenmux" {
		t.Fatalf("job order = %#v", jobs)
	}
}

func TestDecodeRejectsUnknownPolicyField(t *testing.T) {
	t.Parallel()
	input := validConfig + "\n[jobs.openrouter-kimi-code.policy.extra]\nvalue = true\n"
	if _, err := Decode(strings.NewReader(input)); err == nil {
		t.Fatal("unknown policy field was accepted")
	}
}

func TestDiscountedPolicyRequiresValidCeilings(t *testing.T) {
	t.Parallel()
	withoutCeilings := strings.Replace(validConfig, "include_discounted = false", "include_discounted = true", 1)
	if _, err := Decode(strings.NewReader(withoutCeilings)); err == nil {
		t.Fatal("discounted policy without ceilings was accepted")
	}

	withCeilings := strings.Replace(withoutCeilings, "include_discounted = true", "include_discounted = true\nprice_ceilings = { \"prompt|per_token|USD\" = \"0.000001\" }", 1)
	if _, err := Decode(strings.NewReader(withCeilings)); err != nil {
		t.Fatalf("discounted policy with a valid ceiling: %v", err)
	}
}
