package main

import (
	"strings"
	"testing"
)

func TestKimiAssetMappingMatchesSupportedReleaseMatrix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"darwin/amd64":  "kimi-code-darwin-x64.zip",
		"darwin/arm64":  "kimi-code-darwin-arm64.zip",
		"linux/amd64":   "kimi-code-linux-x64.zip",
		"linux/arm64":   "kimi-code-linux-arm64.zip",
		"windows/amd64": "kimi-code-win32-x64.zip",
		"windows/arm64": "kimi-code-win32-arm64.zip",
	}
	for input, wanted := range cases {
		parts := strings.Split(input, "/")
		actual, err := platformAsset(parts[0], parts[1])
		if err != nil || actual != wanted {
			t.Fatalf("platformAsset(%q, %q) = %q, %v", parts[0], parts[1], actual, err)
		}
	}
}

func TestKimiChecksumRequiresTheRequestedAsset(t *testing.T) {
	t.Parallel()
	checksum := strings.Repeat("a", 64)
	actual, err := parseChecksum([]byte(checksum+"  kimi-code-linux-x64.zip\n"), "kimi-code-linux-x64.zip")
	if err != nil || actual != checksum {
		t.Fatalf("checksum = %q, err = %v", actual, err)
	}
	if _, err := parseChecksum([]byte(checksum+"  another.zip\n"), "kimi-code-linux-x64.zip"); err == nil {
		t.Fatal("checksum for another asset was accepted")
	}
}
