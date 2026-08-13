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
	for _, want := range []string{"HighErrorRate", "threshold.scale-up", "for.remove", "mutants across 1 rule file"} {
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
	if !strings.Contains(stdout.String(), "FAIL") || !strings.Contains(stdout.String(), "survived") {
		t.Fatalf("missing failed gate report:\n%s", stdout.String())
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
