package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mtrya/llmloot/internal/model"
	"github.com/pelletier/go-toml/v2"
)

const SchemaVersion = 1

type Config struct {
	SchemaVersion  int               `toml:"schema_version"`
	SourcePriority []string          `toml:"source_priority"`
	Schedule       Schedule          `toml:"schedule"`
	Sources        map[string]Source `toml:"sources"`
	Targets        map[string]Target `toml:"targets"`
	Jobs           map[string]Job    `toml:"jobs"`
}

type Schedule struct {
	Enabled   bool   `toml:"enabled"`
	LocalTime string `toml:"local_time"`
}

type Source struct {
	Adapter       string `toml:"adapter"`
	CredentialEnv string `toml:"credential_env"`
}

type Target struct {
	Adapter string `toml:"adapter"`
}

type Job struct {
	Enabled    bool   `toml:"enabled"`
	Source     string `toml:"source"`
	Target     string `toml:"target"`
	Limit      int    `toml:"limit"`
	MinContext int64  `toml:"min_context"`
	Policy     Policy `toml:"policy"`
}

type Policy struct {
	IncludeFree       bool              `toml:"include_free"`
	IncludeDiscounted bool              `toml:"include_discounted"`
	PriceCeilings     map[string]string `toml:"price_ceilings"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	return Decode(file)
}

func Decode(reader io.Reader) (Config, error) {
	var result Config
	decoder := toml.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	applyDefaults(&result)
	if err := result.Validate(); err != nil {
		return Config{}, err
	}
	return result, nil
}

func Path() (string, error) {
	if home := os.Getenv("LLMLOOT_HOME"); home != "" {
		return filepath.Join(home, "config.toml"), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(root, "llmloot", "config.toml"), nil
}

func StatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "state.json")
}

func LockPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "llmloot.lock")
}

func (configuration Config) Validate() error {
	if configuration.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", configuration.SchemaVersion)
	}
	if len(configuration.Sources) == 0 {
		return errors.New("config has no sources")
	}
	if len(configuration.Targets) == 0 {
		return errors.New("config has no targets")
	}
	if len(configuration.Jobs) == 0 {
		return errors.New("config has no jobs")
	}

	priorities := make(map[string]bool, len(configuration.SourcePriority))
	for _, sourceName := range configuration.SourcePriority {
		if _, exists := configuration.Sources[sourceName]; !exists {
			return fmt.Errorf("source_priority references unknown source %q", sourceName)
		}
		if priorities[sourceName] {
			return fmt.Errorf("source_priority contains duplicate source %q", sourceName)
		}
		priorities[sourceName] = true
	}
	for sourceName, sourceConfig := range configuration.Sources {
		if !priorities[sourceName] {
			return fmt.Errorf("source %q is missing from source_priority", sourceName)
		}
		if sourceConfig.Adapter == "" {
			return fmt.Errorf("source %q has no adapter", sourceName)
		}
		if sourceConfig.CredentialEnv == "" {
			return fmt.Errorf("source %q has no credential_env", sourceName)
		}
	}
	for targetName, targetConfig := range configuration.Targets {
		if targetConfig.Adapter == "" {
			return fmt.Errorf("target %q has no adapter", targetName)
		}
	}

	pairs := make(map[string]string)
	for jobName, job := range configuration.Jobs {
		if _, exists := configuration.Sources[job.Source]; !exists {
			return fmt.Errorf("job %q references unknown source %q", jobName, job.Source)
		}
		if _, exists := configuration.Targets[job.Target]; !exists {
			return fmt.Errorf("job %q references unknown target %q", jobName, job.Target)
		}
		if job.Limit <= 0 {
			return fmt.Errorf("job %q limit must be positive", jobName)
		}
		if job.MinContext <= 0 {
			return fmt.Errorf("job %q min_context must be positive", jobName)
		}
		pair := job.Source + "\x00" + job.Target
		if previous, exists := pairs[pair]; exists {
			return fmt.Errorf("jobs %q and %q bind the same source and target", previous, jobName)
		}
		pairs[pair] = jobName
		if job.Policy.IncludeDiscounted && len(job.Policy.PriceCeilings) == 0 {
			return fmt.Errorf("job %q enables discounted models without price_ceilings", jobName)
		}
		for key, value := range job.Policy.PriceCeilings {
			if err := validateCeiling(key, value); err != nil {
				return fmt.Errorf("job %q: %w", jobName, err)
			}
		}
	}
	return nil
}

func (configuration Config) EnabledJobs(name string) ([]NamedJob, error) {
	if name != "" {
		job, exists := configuration.Jobs[name]
		if !exists {
			return nil, fmt.Errorf("unknown job %q", name)
		}
		if !job.Enabled {
			return nil, fmt.Errorf("job %q is disabled", name)
		}
		return []NamedJob{{Name: name, Job: job}}, nil
	}

	priority := make(map[string]int, len(configuration.SourcePriority))
	for index, sourceName := range configuration.SourcePriority {
		priority[sourceName] = index
	}
	var jobs []NamedJob
	for jobName, job := range configuration.Jobs {
		if job.Enabled {
			jobs = append(jobs, NamedJob{Name: jobName, Job: job})
		}
	}
	sort.Slice(jobs, func(left, right int) bool {
		leftPriority := priority[jobs[left].Source]
		rightPriority := priority[jobs[right].Source]
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return jobs[left].Name < jobs[right].Name
	})
	return jobs, nil
}

type NamedJob struct {
	Name string
	Job
}

func applyDefaults(configuration *Config) {
	for name, job := range configuration.Jobs {
		if job.Limit == 0 {
			job.Limit = 3
		}
		if job.MinContext == 0 {
			job.MinContext = 131072
		}
		configuration.Jobs[name] = job
	}
}

func validateCeiling(key, value string) error {
	parts := strings.Split(key, "|")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return fmt.Errorf("price ceiling key %q must be dimension|unit|currency", key)
	}
	decimal, err := model.ParseDecimal(value)
	if err != nil {
		return fmt.Errorf("price ceiling %q is invalid: %w", key, err)
	}
	if decimal.Sign() < 0 {
		return fmt.Errorf("price ceiling %q must be non-negative", key)
	}
	return nil
}
