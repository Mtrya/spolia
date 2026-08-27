package main

import (
	"strings"
	"testing"
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
