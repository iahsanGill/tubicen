package promtool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
	"github.com/iahsanGill/tubicen/internal/rules"
)

func TestPrepareTestFileExpandsAndReplacesRuleReferences(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	original := filepath.Join(dir, "alerts.yml")
	other := filepath.Join(dir, "recording.yml")
	mutated := filepath.Join(dir, "mutant", "alerts.yml")
	testFile := filepath.Join(dir, "tests.yml")
	destination := filepath.Join(dir, "prepared.yml")

	for _, path := range []string{original, other} {
		if err := os.WriteFile(path, []byte("groups: []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(testFile, []byte("rule_files:\n  - '*.yml'\ntests: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	referencesTarget, err := prepareTestFile(testFile, original, mutated, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !referencesTarget {
		t.Fatal("target rule reference was not detected")
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, mutated) {
		t.Fatalf("mutated rule path missing:\n%s", text)
	}
	if !strings.Contains(text, other) {
		t.Fatalf("other globbed rule path missing:\n%s", text)
	}
	if strings.Contains(text, original) {
		t.Fatalf("original rule was not replaced:\n%s", text)
	}
}

func TestExecuteClassifiesKilledAndSurvivingMutants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	t.Parallel()
	dir := t.TempDir()
	rulePath := filepath.Join(dir, "alerts.yml")
	testPath := filepath.Join(dir, "tests.yml")
	helper := filepath.Join(dir, "promtool")

	ruleText := "groups:\n- name: api\n  rules:\n  - alert: HighErrorRate\n    expr: error_ratio > 0.05\n"
	if err := os.WriteFile(rulePath, []byte(ruleText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("rule_files:\n- alerts.yml\ntests: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "promtool test-double"; exit 0; fi
if [ "$1" = "check" ]; then exit 0; fi
if grep -R -F -q 'error_ratio > 0.5' "$(dirname "$3")"; then
  printf '<testsuites><testsuite tests="1" failures="1" errors="0"><testcase name="alert"><failure>expectation changed</failure></testcase></testsuite></testsuites>' > "$3"
  echo "FAILED: expectation changed"
  exit 1
fi
if grep -R -F -q 'error_ratio > 2' "$(dirname "$3")"; then
  printf '<testsuites><testsuite tests="1" failures="0" errors="1"><testcase name="alert"><error>evaluation failed</error></testcase></testsuite></testsuites>' > "$3"
  echo "FAILED: evaluation failed"
  exit 1
fi
exit 0
`
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	file, err := rules.Load(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	// Process startup is substantially slower under the race detector on CI.
	// This test exercises result classification, not the timeout boundary.
	runner := NewRunner(helper, 15*time.Second)
	killed := runner.Execute(context.Background(), file, domain.Mutation{
		ID: "killed", RuleFile: rulePath, Group: "api", Alert: "HighErrorRate",
		GroupIndex: 0, RuleIndex: 0, Expression: "error_ratio > 0.5",
	}, []string{testPath}, "")
	if killed.Status != domain.StatusKilled {
		t.Fatalf("got status %s, output %q", killed.Status, killed.Output)
	}

	survived := runner.Execute(context.Background(), file, domain.Mutation{
		ID: "survived", RuleFile: rulePath, Group: "api", Alert: "HighErrorRate",
		GroupIndex: 0, RuleIndex: 0, Expression: "error_ratio > 0.025",
	}, []string{testPath}, "")
	if survived.Status != domain.StatusSurvived {
		t.Fatalf("got status %s, output %q", survived.Status, survived.Output)
	}

	failed := runner.Execute(context.Background(), file, domain.Mutation{
		ID: "error", RuleFile: rulePath, Group: "api", Alert: "HighErrorRate",
		GroupIndex: 0, RuleIndex: 0, Expression: "error_ratio > 2",
	}, []string{testPath}, "")
	if failed.Status != domain.StatusError || !strings.Contains(failed.Output, "test execution error") {
		t.Fatalf("unexpected execution failure: %#v", failed)
	}
}

func TestRunnerClassifiesTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, "promtool")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nsleep 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(helper, 20*time.Millisecond)
	if _, err := runner.Version(context.Background()); !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestExecuteRejectsSuitesThatDoNotReferenceTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	dir := t.TempDir()
	rulePath := filepath.Join(dir, "alerts.yml")
	otherPath := filepath.Join(dir, "other.yml")
	testPath := filepath.Join(dir, "tests.yml")
	helper := filepath.Join(dir, "promtool")
	if err := os.WriteFile(rulePath, []byte("groups:\n- name: api\n  rules:\n  - alert: Down\n    expr: up == 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("groups: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("rule_files:\n- other.yml\ntests: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := rules.Load(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	result := NewRunner(helper, 15*time.Second).Execute(context.Background(), file, domain.Mutation{
		ID: "unreferenced", RuleFile: rulePath, Group: "api", Alert: "Down",
		GroupIndex: 0, RuleIndex: 0, Expression: "up != 0",
	}, []string{testPath}, "")
	if result.Status != domain.StatusError || !strings.Contains(result.Output, "no supplied test file references") {
		t.Fatalf("unexpected result: %#v", result)
	}
}
