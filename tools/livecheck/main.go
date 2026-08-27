package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/app"
	"github.com/Mtrya/llmloot/internal/atomicfile"
	"github.com/Mtrya/llmloot/internal/config"
)

const reportSchemaVersion = 1

type report struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Outcome       string       `json:"outcome"`
	Cells         []cellReport `json:"cells"`
}

type cellReport struct {
	Source           string            `json:"source"`
	Policy           string            `json:"policy"`
	PriceCeilings    map[string]string `json:"price_ceilings,omitempty"`
	SelectedModel    string            `json:"selected_model,omitempty"`
	EligibilityClass string            `json:"eligibility_class,omitempty"`
	LLMlootVersion   string            `json:"llmloot_version"`
	KimiCodeVersion  string            `json:"kimi_code_version"`
	OS               string            `json:"os"`
	Architecture     string            `json:"architecture"`
	SetupOutcome     string            `json:"setup_outcome,omitempty"`
	SyncOutcome      string            `json:"sync_outcome,omitempty"`
	ModelActivation  string            `json:"model_activation,omitempty"`
	ToolUseSuccess   bool              `json:"tool_use_success"`
	Error            string            `json:"error,omitempty"`
}

type policyFlags struct {
	name     string
	ceilings map[string]string
}

type ceilingFlag map[string]string

func (value *ceilingFlag) String() string {
	if value == nil || len(*value) == 0 {
		return ""
	}
	keys := make([]string, 0, len(*value))
	for key := range *value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+(*value)[key])
	}
	return strings.Join(parts, ",")
}

func (value *ceilingFlag) Set(text string) error {
	key, amount, found := strings.Cut(text, "=")
	if !found || key == "" || amount == "" {
		return fmt.Errorf("ceiling must be dimension|unit|currency=value")
	}
	if *value == nil {
		*value = make(map[string]string)
	}
	if _, exists := (*value)[key]; exists {
		return fmt.Errorf("duplicate ceiling %q", key)
	}
	(*value)[key] = amount
	return nil
}

func main() {
	sourceName := flag.String("source", "all", "cell to run: openrouter, zenmux, or all")
	selectedModel := flag.String("model", "", "exact eligible model to validate; requires a single source")
	llmlootFlag := flag.String("llmloot", "llmloot", "llmloot binary to validate")
	kimiFlag := flag.String("kimi", "kimi", "Kimi Code binary to use")
	openRouterPolicy := flag.String("openrouter-policy", "stealth", "openrouter policy: stealth, free, discounted, or free+discounted")
	zenMuxPolicy := flag.String("zenmux-policy", "stealth", "zenmux policy: stealth, free, discounted, or free+discounted")
	var openRouterCeilings, zenMuxCeilings ceilingFlag
	flag.Var(&openRouterCeilings, "openrouter-ceiling", "repeatable OpenRouter ceiling dimension|unit|currency=value")
	flag.Var(&zenMuxCeilings, "zenmux-ceiling", "repeatable ZenMux ceiling dimension|unit|currency=value")
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if runtime.GOOS != "linux" {
		fatal(errors.New("the release live check must run on Linux"))
	}
	sources, err := requestedSources(*sourceName)
	if err != nil {
		fatal(err)
	}
	if *selectedModel != "" && len(sources) != 1 {
		fatal(errors.New("an exact model requires a single source"))
	}
	policies := map[string]policyFlags{
		"openrouter": {name: *openRouterPolicy, ceilings: openRouterCeilings},
		"zenmux":     {name: *zenMuxPolicy, ceilings: zenMuxCeilings},
	}
	for _, source := range sources {
		if _, err := configuredPolicy(policies[source]); err != nil {
			fatal(fmt.Errorf("%s: %w", source, err))
		}
		if os.Getenv(credentialEnvironment(source)) == "" {
			fatal(fmt.Errorf("%s credential is unavailable", source))
		}
	}
	llmloot, err := executablePath(*llmlootFlag)
	if err != nil {
		fatal(err)
	}
	kimi, err := executablePath(*kimiFlag)
	if err != nil {
		fatal(err)
	}
	llmlootVersion, err := commandVersion(llmloot)
	if err != nil {
		fatal(err)
	}
	kimiVersion, err := commandVersion(kimi)
	if err != nil {
		fatal(err)
	}

	result := report{SchemaVersion: reportSchemaVersion, GeneratedAt: time.Now().UTC(), Outcome: "pass", Cells: make([]cellReport, 0, len(sources))}
	for _, source := range sources {
		cell := runCell(source, policies[source], *selectedModel, llmloot, kimi, llmlootVersion, kimiVersion)
		if !cell.ToolUseSuccess {
			result.Outcome = "fail"
		}
		result.Cells = append(result.Cells, cell)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fatal(err)
	}
	if result.Outcome != "pass" {
		os.Exit(1)
	}
}

func runCell(sourceName string, policy policyFlags, requestedModel, llmloot, kimi, llmlootVersion, kimiVersion string) cellReport {
	cell := cellReport{
		Source: sourceName, Policy: policy.name, PriceCeilings: cloneMap(policy.ceilings), LLMlootVersion: llmlootVersion,
		KimiCodeVersion: kimiVersion, OS: runtime.GOOS, Architecture: runtime.GOARCH,
	}
	credential := os.Getenv(credentialEnvironment(sourceName))
	root, err := os.MkdirTemp("", "llmloot-livecheck-")
	if err != nil {
		cell.Error = "create isolated live-check directory"
		return cell
	}
	defer os.RemoveAll(root)
	redact := newRedactor(credential, root)
	llmlootHome := filepath.Join(root, "llmloot")
	kimiHome := filepath.Join(root, "kimi")
	workspace := filepath.Join(root, "workspace")
	skills := filepath.Join(root, "skills")
	for _, path := range []string{workspace, skills} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			cell.Error = redact(err.Error())
			return cell
		}
	}
	configuration := config.Default()
	configuration.Schedule.Enabled = false
	for name, job := range configuration.Jobs {
		job.Enabled = job.Source == sourceName
		if job.Enabled {
			configured, err := configuredPolicy(policy)
			if err != nil {
				cell.Error = redact(err.Error())
				return cell
			}
			job.Policy = configured
		}
		configuration.Jobs[name] = job
	}
	if err := config.Save(filepath.Join(llmlootHome, "config.toml"), configuration); err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	baseEnvironment := isolatedEnvironment(llmlootHome, kimiHome, kimi)
	setupEnvironment := append(append([]string(nil), baseEnvironment...), credentialEnvironment(sourceName)+"="+credential)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	setupOutput, err := runCommand(ctx, workspace, setupEnvironment, llmloot, "setup", "--yes", "--no-schedule", "--json")
	if err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	if bytes.Contains(setupOutput, []byte(credential)) {
		cell.Error = "credential appeared in setup output"
		return cell
	}
	var setup app.SyncResult
	if err := json.Unmarshal(setupOutput, &setup); err != nil {
		cell.Error = "decode setup output"
		return cell
	}
	cell.SetupOutcome = setup.Outcome
	if setup.Outcome != "success" {
		cell.Error = "setup did not complete successfully"
		return cell
	}
	syncOutput, err := runCommand(ctx, workspace, baseEnvironment, llmloot, "sync", "--json")
	if err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	if bytes.Contains(syncOutput, []byte(credential)) {
		cell.Error = "credential appeared in sync output"
		return cell
	}
	var syncResult app.SyncResult
	if err := json.Unmarshal(syncOutput, &syncResult); err != nil {
		cell.Error = "decode sync output"
		return cell
	}
	cell.SyncOutcome = syncResult.Outcome
	if syncResult.Outcome != "success" || len(syncResult.Jobs) != 1 || len(syncResult.Jobs[0].Selected) == 0 {
		cell.Error = "no model was selected under the explicit policy"
		return cell
	}
	selected, err := selectLiveModel(syncResult.Jobs[0].Selected, requestedModel)
	if err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	cell.SelectedModel = selected.ID
	cell.EligibilityClass = string(selected.Class)
	if err := activateIsolatedKimiModel(filepath.Join(kimiHome, "config.toml"), selected.ID); err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	cell.ModelActivation = "isolated_default"
	prompt := "Use the Shell tool exactly once to run: printf llmloot-livecheck-tool-ok. After the tool succeeds, reply with a brief confirmation."
	stream, err := runCommand(ctx, workspace, baseEnvironment, kimi, "-m", selected.ID, "-p", prompt, "--output-format", "stream-json", "--skills-dir", skills)
	if err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	if bytes.Contains(stream, []byte(credential)) {
		cell.Error = "credential appeared in Kimi Code output"
		return cell
	}
	success, err := toolUseSucceeded(stream, "llmloot-livecheck-tool-ok")
	if err != nil {
		cell.Error = redact(err.Error())
		return cell
	}
	cell.ToolUseSuccess = success
	if !success {
		cell.Error = "Kimi Code did not complete the required harmless tool-use turn"
	}
	return cell
}

func selectLiveModel(selected []app.SelectedModel, requested string) (app.SelectedModel, error) {
	if len(selected) == 0 {
		return app.SelectedModel{}, errors.New("no model was selected under the explicit policy")
	}
	if requested == "" {
		return selected[0], nil
	}
	for _, candidate := range selected {
		if candidate.ID == requested {
			return candidate, nil
		}
	}
	return app.SelectedModel{}, fmt.Errorf("requested model %q was not selected under the explicit policy", requested)
}

// Kimi Code 0.38 and 0.39 bind -m to the session but check default_model at
// their prompt readiness gate. The live check owns and deletes this isolated
// home, so it activates the selected alias there without touching user config.
func activateIsolatedKimiModel(path, alias string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read isolated Kimi Code config: %w", err)
	}
	prefix := []byte("default_model = " + strconv.Quote(alias) + "\n\n")
	if err := atomicfile.Write(path, append(prefix, contents...), 0o600); err != nil {
		return fmt.Errorf("activate selected model in isolated Kimi Code config: %w", err)
	}
	return nil
}

func requestedSources(name string) ([]string, error) {
	switch name {
	case "all":
		return []string{"openrouter", "zenmux"}, nil
	case "openrouter", "zenmux":
		return []string{name}, nil
	default:
		return nil, fmt.Errorf("unknown source %q", name)
	}
}

func configuredPolicy(flags policyFlags) (config.Policy, error) {
	policy := config.Policy{PriceCeilings: cloneMap(flags.ceilings)}
	switch flags.name {
	case "stealth":
	case "free":
		policy.IncludeFree = true
	case "discounted":
		policy.IncludeDiscounted = true
	case "free+discounted":
		policy.IncludeFree = true
		policy.IncludeDiscounted = true
	default:
		return config.Policy{}, fmt.Errorf("unknown policy %q", flags.name)
	}
	if policy.IncludeDiscounted && len(policy.PriceCeilings) == 0 {
		return config.Policy{}, errors.New("discounted policy requires at least one explicit price ceiling")
	}
	if !policy.IncludeDiscounted && len(policy.PriceCeilings) != 0 {
		return config.Policy{}, errors.New("price ceilings require a discounted policy")
	}
	return policy, nil
}

func toolUseSucceeded(stream []byte, marker string) (bool, error) {
	type toolCall struct {
		ID       string `json:"id"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	var message struct {
		Role       string          `json:"role"`
		ToolCallID string          `json:"tool_call_id"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []toolCall      `json:"tool_calls"`
	}
	toolIDs := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		message = struct {
			Role       string          `json:"role"`
			ToolCallID string          `json:"tool_call_id"`
			Content    json.RawMessage `json:"content"`
			ToolCalls  []toolCall      `json:"tool_calls"`
		}{}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return false, fmt.Errorf("decode Kimi Code stream: %w", err)
		}
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				name := strings.ToLower(call.Function.Name)
				if name == "shell" || name == "bash" {
					toolIDs[call.ID] = true
				}
			}
		}
		if message.Role == "tool" && toolIDs[message.ToolCallID] && bytes.Contains(message.Content, []byte(marker)) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read Kimi Code stream: %w", err)
	}
	return false, nil
}

func credentialEnvironment(sourceName string) string {
	if sourceName == "openrouter" {
		return "OPENROUTER_API_KEY"
	}
	return "ZENMUX_API_KEY"
}

func isolatedEnvironment(llmlootHome, kimiHome, kimi string) []string {
	blocked := map[string]bool{
		"OPENROUTER_API_KEY": true, "ZENMUX_API_KEY": true, "LLMLOOT_HOME": true, "KIMI_CODE_HOME": true,
		"LLMLOOT_TEST_OPENROUTER_MODELS_ENDPOINT": true, "LLMLOOT_TEST_ZENMUX_MODELS_ENDPOINT": true,
		"KIMI_DISABLE_TELEMETRY": true, "PATH": true,
	}
	result := make([]string, 0, len(os.Environ())+4)
	pathValue := ""
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if name == "PATH" {
			pathValue = value
		}
		if !blocked[name] {
			result = append(result, entry)
		}
	}
	return append(result,
		"LLMLOOT_HOME="+llmlootHome,
		"KIMI_CODE_HOME="+kimiHome,
		"KIMI_DISABLE_TELEMETRY=1",
		"PATH="+filepath.Dir(kimi)+string(os.PathListSeparator)+pathValue,
	)
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

func commandVersion(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read %s version: %w", filepath.Base(binary), err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", fmt.Errorf("%s returned an empty version", filepath.Base(binary))
	}
	return version, nil
}

func runCommand(ctx context.Context, directory string, environment []string, binary string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = directory
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%s exited with %v: %s", filepath.Base(binary), err, compact(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func newRedactor(values ...string) func(string) string {
	return func(message string) string {
		for _, value := range values {
			if value != "" {
				message = strings.ReplaceAll(message, value, "[redacted]")
			}
		}
		return compact(message)
	}
}

func compact(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func cloneMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
