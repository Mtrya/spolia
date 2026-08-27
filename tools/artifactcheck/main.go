package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/config"
)

const artifactCredential = "llmloot-artifact-check-key"

func main() {
	binary := flag.String("binary", "", "llmloot release binary")
	binDir := flag.String("bin-dir", "", "directory containing the llmloot release binary")
	kimi := flag.String("kimi", "kimi", "Kimi Code binary")
	sourceName := flag.String("source", "all", "fixture source: openrouter, zenmux, or all")
	flag.Parse()
	if flag.NArg() != 0 || (*binary == "" && *binDir == "") {
		flag.Usage()
		os.Exit(2)
	}
	if *binary == "" {
		name := "llmloot"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		*binary = filepath.Join(*binDir, name)
	}
	resolvedBinary, err := executablePath(*binary)
	if err != nil {
		fail(err)
	}
	resolvedKimi, err := executablePath(*kimi)
	if err != nil {
		fail(err)
	}
	sources := []string{"openrouter", "zenmux"}
	if *sourceName != "all" {
		if *sourceName != "openrouter" && *sourceName != "zenmux" {
			fail(fmt.Errorf("unknown source %q", *sourceName))
		}
		sources = []string{*sourceName}
	}
	versionOutput, err := exec.Command(resolvedBinary, "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(versionOutput)) == "" {
		fail(fmt.Errorf("release binary version failed: %w", err))
	}
	for _, source := range sources {
		if err := checkSource(resolvedBinary, resolvedKimi, source); err != nil {
			fail(err)
		}
	}
	fmt.Printf("artifact check passed for %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func executablePath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("find executable %q: %w", name, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", name, err)
	}
	return path, nil
}

func checkSource(binary, kimi, sourceName string) error {
	fixturePath := filepath.Join("internal", "testdata", sourceName+"-models.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		return fmt.Errorf("read %s fixture: %w", sourceName, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+artifactCredential {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(fixture)
	}))
	defer server.Close()
	root, err := os.MkdirTemp("", "llmloot-artifact-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	llmlootHome := filepath.Join(root, "llmloot")
	kimiHome := filepath.Join(root, "kimi")
	configuration := config.Default()
	configuration.Schedule.Enabled = false
	for name, job := range configuration.Jobs {
		job.Enabled = job.Source == sourceName
		if sourceName == "zenmux" && job.Enabled {
			job.Policy.IncludeFree = true
		}
		configuration.Jobs[name] = job
	}
	if err := config.Save(filepath.Join(llmlootHome, "config.toml"), configuration); err != nil {
		return err
	}
	environment := artifactEnvironment(sourceName, server.URL, llmlootHome, kimiHome, kimi)
	setup, err := runJSON(binary, environment, "setup", "--yes", "--no-schedule", "--json")
	if err != nil {
		return fmt.Errorf("%s setup failed: %w", sourceName, err)
	}
	if setup.Outcome != "success" || len(setup.Jobs) != 1 || len(setup.Jobs[0].Selected) == 0 {
		return fmt.Errorf("%s setup did not select the fixture model", sourceName)
	}
	dryRun, err := runJSON(binary, environment, "sync", "--dry-run", "--json")
	if err != nil {
		return fmt.Errorf("%s fixture dry-run failed: %w", sourceName, err)
	}
	if dryRun.Outcome != "success" || !dryRun.DryRun || len(dryRun.Jobs) != 1 || len(dryRun.Jobs[0].Selected) == 0 {
		return fmt.Errorf("%s fixture dry-run returned an unexpected decision", sourceName)
	}
	doctorOutput, err := run(binary, environment, "doctor", "--json")
	if err != nil {
		return fmt.Errorf("%s doctor failed: %w", sourceName, err)
	}
	var doctor struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(doctorOutput, &doctor); err != nil || doctor.Outcome != "healthy" {
		return fmt.Errorf("%s doctor returned an unhealthy result", sourceName)
	}
	return nil
}

func runJSON(binary string, environment []string, arguments ...string) (app.SyncResult, error) {
	contents, err := run(binary, environment, arguments...)
	if err != nil {
		return app.SyncResult{}, err
	}
	if strings.Contains(string(contents), artifactCredential) {
		return app.SyncResult{}, fmt.Errorf("credential appeared in command output")
	}
	var result app.SyncResult
	if err := json.Unmarshal(contents, &result); err != nil {
		return app.SyncResult{}, fmt.Errorf("decode command output: %w", err)
	}
	return result, nil
}

func run(binary string, environment []string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = environment
	contents, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("command exited %d: %s", exitError.ExitCode(), compact(exitError.Stderr))
		}
		return nil, err
	}
	return contents, nil
}

func artifactEnvironment(sourceName, endpoint, llmlootHome, kimiHome, kimi string) []string {
	blocked := map[string]bool{
		"OPENROUTER_API_KEY": true, "ZENMUX_API_KEY": true, "LLMLOOT_HOME": true, "KIMI_CODE_HOME": true,
		"LLMLOOT_TEST_OPENROUTER_MODELS_ENDPOINT": true, "LLMLOOT_TEST_ZENMUX_MODELS_ENDPOINT": true,
		"KIMI_DISABLE_TELEMETRY": true, "PATH": true,
	}
	environment := make([]string, 0, len(os.Environ())+6)
	pathValue := ""
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if name == "PATH" {
			pathValue = value
		}
		if !blocked[name] {
			environment = append(environment, entry)
		}
	}
	environment = append(environment,
		"LLMLOOT_HOME="+llmlootHome,
		"KIMI_CODE_HOME="+kimiHome,
		"KIMI_DISABLE_TELEMETRY=1",
		"PATH="+filepath.Dir(kimi)+string(os.PathListSeparator)+pathValue,
	)
	if sourceName == "openrouter" {
		environment = append(environment, "OPENROUTER_API_KEY="+artifactCredential, "LLMLOOT_TEST_OPENROUTER_MODELS_ENDPOINT="+endpoint)
	} else {
		environment = append(environment, "ZENMUX_API_KEY="+artifactCredential, "LLMLOOT_TEST_ZENMUX_MODELS_ENDPOINT="+endpoint)
	}
	return environment
}

func compact(contents []byte) string {
	text := strings.Join(strings.Fields(string(contents)), " ")
	text = strings.ReplaceAll(text, artifactCredential, "[redacted]")
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
