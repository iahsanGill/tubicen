package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings is the repository-level policy used by the check command.
type Settings struct {
	Rules        []string `yaml:"rules"`
	Tests        []string `yaml:"tests"`
	Promtool     string   `yaml:"promtool"`
	Workers      int      `yaml:"workers"`
	Timeout      string   `yaml:"timeout"`
	Threshold    *float64 `yaml:"threshold"`
	Only         []string `yaml:"only"`
	Skip         []string `yaml:"skip"`
	Limit        int      `yaml:"limit"`
	SurvivorsDir string   `yaml:"survivors"`
	Quiet        bool     `yaml:"quiet"`
	Reports      Reports  `yaml:"reports"`
}

// Reports contains optional CI artifact paths.
type Reports struct {
	JSON  string `yaml:"json"`
	JUnit string `yaml:"junit"`
	SARIF string `yaml:"sarif"`
	HTML  string `yaml:"html"`
}

// Load reads a Tubicen policy file and resolves file paths relative to it.
func Load(path string) (Settings, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Settings{}, fmt.Errorf("resolve config %q: %w", path, err)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return Settings{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var settings Settings
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Settings{}, fmt.Errorf("parse config %q: %w", path, err)
		}
		return Settings{}, fmt.Errorf("parse config %q: only one YAML document is allowed", path)
	}
	if len(settings.Rules) == 0 {
		return Settings{}, fmt.Errorf("config %q must list at least one rule file", path)
	}
	if len(settings.Tests) == 0 {
		return Settings{}, fmt.Errorf("config %q must list at least one test file", path)
	}
	if settings.Timeout != "" {
		if _, err := time.ParseDuration(settings.Timeout); err != nil {
			return Settings{}, fmt.Errorf("config %q has invalid timeout %q: %w", path, settings.Timeout, err)
		}
	}
	if settings.Threshold != nil && (*settings.Threshold < 0 || *settings.Threshold > 100) {
		return Settings{}, fmt.Errorf("config %q threshold must be between 0 and 100", path)
	}
	if settings.Workers < 0 {
		return Settings{}, fmt.Errorf("config %q workers must not be negative", path)
	}
	if settings.Limit < 0 {
		return Settings{}, fmt.Errorf("config %q limit must not be negative", path)
	}
	base := filepath.Dir(absolute)
	settings.Rules = resolveAll(base, settings.Rules)
	settings.Tests = resolveAll(base, settings.Tests)
	if settings.Promtool != "" && !filepath.IsAbs(settings.Promtool) && strings.ContainsAny(settings.Promtool, `/\`) {
		settings.Promtool = filepath.Clean(filepath.Join(base, settings.Promtool))
	}
	settings.SurvivorsDir = resolveOutput(base, settings.SurvivorsDir)
	settings.Reports.JSON = resolveOutput(base, settings.Reports.JSON)
	settings.Reports.JUnit = resolveOutput(base, settings.Reports.JUnit)
	settings.Reports.SARIF = resolveOutput(base, settings.Reports.SARIF)
	settings.Reports.HTML = resolveOutput(base, settings.Reports.HTML)
	return settings, nil
}

func resolveAll(base string, paths []string) []string {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		resolved = append(resolved, filepath.Clean(path))
	}
	return resolved
}

func resolveOutput(base, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(base, path))
}
