package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/pelletier/go-toml/v2"
)

func TestLivePoliciesRequireExplicitWideningAndCeilings(t *testing.T) {
	t.Parallel()
	stealth, err := configuredPolicy(policyFlags{name: "stealth"})
	if err != nil || stealth.IncludeFree || stealth.IncludeDiscounted {
		t.Fatalf("stealth = %#v, err = %v", stealth, err)
	}
	free, err := configuredPolicy(policyFlags{name: "free"})
	if err != nil || !free.IncludeFree || free.IncludeDiscounted {
		t.Fatalf("free = %#v, err = %v", free, err)
	}
	if _, err := configuredPolicy(policyFlags{name: "discounted"}); err == nil {
		t.Fatal("discounted policy without ceilings was accepted")
	}
	discounted, err := configuredPolicy(policyFlags{name: "discounted", ceilings: map[string]string{"prompt|per_token|USD": "0.1"}})
	if err != nil || discounted.IncludeFree || !discounted.IncludeDiscounted {
		t.Fatalf("discounted = %#v, err = %v", discounted, err)
	}
}

func TestToolUseRequiresAMatchingShellResult(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"Shell","arguments":"{\"command\":\"printf marker\"}"}}]}`,
		`{"role":"tool","tool_call_id":"call-1","content":"marker"}`,
		`{"role":"assistant","content":"done"}`,
	}, "\n")
	success, err := toolUseSucceeded([]byte(stream), "marker")
	if err != nil || !success {
		t.Fatalf("success = %t, err = %v", success, err)
	}
	wrongTool := strings.Replace(stream, `"Shell"`, `"Read"`, 1)
	if success, err := toolUseSucceeded([]byte(wrongTool), "marker"); err != nil || success {
		t.Fatalf("wrong tool success = %t, err = %v", success, err)
	}
}

func TestLiveErrorRedactionRemovesCredentialAndWorkspace(t *testing.T) {
	t.Parallel()
	redact := newRedactor("provider-secret", "/tmp/private-livecheck")
	message := redact("request provider-secret failed in /tmp/private-livecheck/config.toml")
	if strings.Contains(message, "provider-secret") || strings.Contains(message, "/tmp/private-livecheck") {
		t.Fatalf("redacted message = %q", message)
	}
}

func TestActivateIsolatedKimiModel(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := []byte("[models.\"provider/model\"]\nprovider = \"provider\"\nmodel = \"provider/model\"\n")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := activateIsolatedKimiModel(path, "provider/model"); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	if document["default_model"] != "provider/model" {
		t.Fatalf("default_model = %#v", document["default_model"])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestSelectLiveModelRequiresAnEligibleSelection(t *testing.T) {
	t.Parallel()
	selected := []app.SelectedModel{{ID: "first/model"}, {ID: "second/model"}}
	model, err := selectLiveModel(selected, "second/model")
	if err != nil || model.ID != "second/model" {
		t.Fatalf("model = %#v, err = %v", model, err)
	}
	if _, err := selectLiveModel(selected, "unselected/model"); err == nil {
		t.Fatal("unselected model was accepted")
	}
}
