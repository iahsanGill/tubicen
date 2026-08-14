package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute(context.Background(), []string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "tubicen dev") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestListMutations(t *testing.T) {
	temp := t.TempDir()
	rulesPath := filepath.Join(temp, "alerts.yml")
	content := `groups:
- name: api
  rules:
  - alert: HighErrorRate
    expr: rate(requests_total{status="500"}[5m]) > 0.05
    for: 5m
`
	if err := os.WriteFile(rulesPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), []string{"list", "--rules", rulesPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{"HighErrorRate", "Alert threshold", "Time before alerting", "rule changes across 1 rule file"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestMissingCommandIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute(context.Background(), nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("missing usage: %s", stderr.String())
	}
}

func TestRunRequiresInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := execute(context.Background(), []string{"run"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "both --rules and --tests are required") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}

func TestRunRejectsInvalidExecutionFlags(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		flag string
		want string
	}{
		{name: "workers", flag: "--workers=-1", want: "--workers must not be negative"},
		{name: "timeout", flag: "--timeout=-1s", want: "--timeout must be positive"},
		{name: "limit", flag: "--limit=-1", want: "--limit must not be negative"},
		{name: "threshold", flag: "--threshold=101", want: "--threshold must be between 0 and 100"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := execute(context.Background(), []string{"run", "--rules", "alerts.yml", "--tests", "tests.yml", test.flag}, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
		})
	}
}

func TestRunReturnsOneWhenMutationGateFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	temp := t.TempDir()
	rulesPath := filepath.Join(temp, "alerts.yml")
	testsPath := filepath.Join(temp, "alerts_test.yml")
	promtoolPath := filepath.Join(temp, "promtool")

	mustWrite(t, rulesPath, `groups:
- name: api
  rules:
  - alert: HighLatency
    expr: request_latency_seconds > 1
`)
	mustWrite(t, testsPath, `rule_files:
- alerts.yml
tests: []
`)
	mustWrite(t, promtoolPath, `#!/bin/sh
if [ "$1" = "--version" ]; then echo "promtool test-double"; fi
exit 0
`)
	if err := os.Chmod(promtoolPath, 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), []string{
		"run", "--rules", rulesPath, "--tests", testsPath,
		"--promtool", promtoolPath, "--threshold", "100", "--quiet",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s\nstdout = %s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAIL") || !strings.Contains(stdout.String(), "not caught") {
		t.Fatalf("missing failed gate report:\n%s", stdout.String())
	}
}

func TestCheckLoadsRepositoryPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	temp := t.TempDir()
	mustWrite(t, filepath.Join(temp, "alerts.yml"), `groups:
- name: api
  rules:
  - alert: HighLatency
    expr: request_latency_seconds > 1
`)
	mustWrite(t, filepath.Join(temp, "alerts_test.yml"), `rule_files:
- alerts.yml
tests: []
`)
	promtoolPath := filepath.Join(temp, "promtool")
	mustWrite(t, promtoolPath, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"promtool test-double\"; fi\nexit 0\n")
	if err := os.Chmod(promtoolPath, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(temp, ".tubicen.yml"), `rules: [alerts.yml]
tests: [alerts_test.yml]
promtool: ./promtool
threshold: 0
quiet: true
reports:
  json: artifacts/report.json
`)

	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), []string{"check", "--config", filepath.Join(temp, ".tubicen.yml")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %s\nstdout = %s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "PASS") {
		t.Fatalf("missing passed gate report:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(temp, "artifacts", "report.json")); err != nil {
		t.Fatalf("JSON report was not written: %v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
