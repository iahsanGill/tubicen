package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesRepositoryPaths(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, ".tubicen.yml")
	content := `rules:
  - rules/alerts.yml
tests:
  - tests/alerts.yml
threshold: 95
timeout: 45s
only: [aggregation, threshold]
reports:
  json: artifacts/report.json
  sarif: artifacts/report.sarif
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := settings.Rules[0], filepath.Join(directory, "rules", "alerts.yml"); got != want {
		t.Fatalf("rule path = %q, want %q", got, want)
	}
	if got, want := settings.Tests[0], filepath.Join(directory, "tests", "alerts.yml"); got != want {
		t.Fatalf("test path = %q, want %q", got, want)
	}
	if got, want := settings.Reports.SARIF, filepath.Join(directory, "artifacts", "report.sarif"); got != want {
		t.Fatalf("SARIF path = %q, want %q", got, want)
	}
	if settings.Threshold == nil || *settings.Threshold != 95 {
		t.Fatalf("threshold = %v", settings.Threshold)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".tubicen.yml")
	if err := os.WriteFile(path, []byte("rules: [rules.yml]\ntests: [tests.yml]\nthereshold: 90\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field thereshold not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRequiresRulesAndTests(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".tubicen.yml")
	if err := os.WriteFile(path, []byte("rules: [rules.yml]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "at least one test file") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".tubicen.yml")
	content := "rules: [rules.yml]\ntests: [tests.yml]\n---\nrules: [other.yml]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "only one YAML document") {
		t.Fatalf("error = %v", err)
	}
}
