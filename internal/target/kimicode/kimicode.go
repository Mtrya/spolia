package kimicode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const MinimumVersion = "0.38.0"

type Installation struct {
	Binary     string `json:"binary"`
	Version    string `json:"version"`
	Home       string `json:"home"`
	ConfigPath string `json:"config_path"`
}

type ProviderSpec struct {
	ID        string
	Type      string
	BaseURL   string
	APIKeyEnv string
}

var providerSpecs = map[string]ProviderSpec{
	"openrouter": {ID: "openrouter", Type: "openai", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"},
	"zenmux":     {ID: "zenmux", Type: "openai", BaseURL: "https://zenmux.ai/api/v1", APIKeyEnv: "ZENMUX_API_KEY"},
}

func ProviderForSource(sourceName string) (ProviderSpec, error) {
	specification, exists := providerSpecs[sourceName]
	if !exists {
		return ProviderSpec{}, fmt.Errorf("Kimi Code has no provider mapping for source %q", sourceName)
	}
	return specification, nil
}

func Discover(ctx context.Context) (Installation, error) {
	binary, err := exec.LookPath("kimi")
	if err != nil {
		return Installation{}, errors.New("Kimi Code executable not found in PATH")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return Installation{}, fmt.Errorf("resolve Kimi Code executable: %w", err)
	}
	home := os.Getenv("KIMI_CODE_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Installation{}, fmt.Errorf("resolve home directory: %w", err)
		}
		home = filepath.Join(userHome, ".kimi-code")
	}
	return InspectInstallation(ctx, binary, home)
}

func InspectInstallation(ctx context.Context, binary, home string) (Installation, error) {
	command := exec.CommandContext(ctx, binary, "--version")
	output, err := command.Output()
	if err != nil {
		return Installation{}, fmt.Errorf("run Kimi Code version check: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if compareVersions(version, MinimumVersion) < 0 {
		return Installation{}, fmt.Errorf("Kimi Code %s is unsupported; version %s or newer is required", version, MinimumVersion)
	}
	absoluteHome, err := filepath.Abs(home)
	if err != nil {
		return Installation{}, fmt.Errorf("resolve Kimi Code home: %w", err)
	}
	return Installation{Binary: binary, Version: version, Home: absoluteHome, ConfigPath: filepath.Join(absoluteHome, "config.toml")}, nil
}

var semanticVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

func compareVersions(left, right string) int {
	leftParts := semanticVersionPattern.FindStringSubmatch(left)
	rightParts := semanticVersionPattern.FindStringSubmatch(right)
	if leftParts == nil || rightParts == nil {
		return -1
	}
	for index := 1; index <= 3; index++ {
		leftValue, _ := strconv.Atoi(leftParts[index])
		rightValue, _ := strconv.Atoi(rightParts[index])
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func (installation Installation) Validate(ctx context.Context, path string) error {
	command := exec.CommandContext(ctx, installation.Binary, "doctor", "config", path)
	command.Env = append(withoutEnvironment(os.Environ(), "KIMI_CODE_HOME", "KIMI_DISABLE_TELEMETRY"), "KIMI_CODE_HOME="+installation.Home, "KIMI_DISABLE_TELEMETRY=1")
	if err := command.Run(); err != nil {
		return errors.New("Kimi Code rejected the generated configuration")
	}
	return nil
}

func withoutEnvironment(environment []string, names ...string) []string {
	blocked := make(map[string]bool, len(names))
	for _, name := range names {
		blocked[name] = true
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			result = append(result, entry)
		}
	}
	return result
}
