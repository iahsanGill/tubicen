package campaign

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
)

func TestSummarize(t *testing.T) {
	t.Parallel()
	results := []domain.Result{
		{Status: domain.StatusKilled},
		{Status: domain.StatusKilled},
		{Status: domain.StatusKilled},
		{Status: domain.StatusSurvived},
	}
	summary := summarize(results, 75)
	if summary.Score != 75 || !summary.PassedGate {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	results = append(results, domain.Result{Status: domain.StatusError})
	summary = summarize(results, 75)
	if summary.PassedGate {
		t.Fatalf("campaign with execution errors passed: %#v", summary)
	}
}

func TestOperatorSelectedSupportsFamilies(t *testing.T) {
	t.Parallel()
	if !operatorSelected("threshold.scale-up", []string{"threshold"}, nil) {
		t.Fatal("operator family did not match")
	}
	if operatorSelected("threshold.scale-up", nil, []string{"threshold"}) {
		t.Fatal("skipped operator was selected")
	}
	if operatorSelected("range.expand", []string{"threshold"}, nil) {
		t.Fatal("unrelated operator was selected")
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	for _, options := range []Options{
		{RuleFiles: []string{"rules.yml"}, TestFiles: []string{"tests.yml"}, Workers: -1},
		{RuleFiles: []string{"rules.yml"}, TestFiles: []string{"tests.yml"}, Timeout: -time.Second},
		{RuleFiles: []string{"rules.yml"}, TestFiles: []string{"tests.yml"}, Limit: -1},
	} {
		if _, err := Run(context.Background(), options); err == nil {
			t.Fatalf("invalid options accepted: %#v", options)
		}
	}
}

func TestRunExecutesSelectedMutations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	t.Parallel()
	dir := t.TempDir()
	rulePath := filepath.Join(dir, "alerts.yml")
	testPath := filepath.Join(dir, "tests.yml")
	promtoolPath := filepath.Join(dir, "promtool")
	files := map[string]string{
		rulePath: "groups:\n- name: api\n  rules:\n  - alert: Down\n    expr: up == 0\n",
		testPath: "rule_files:\n- alerts.yml\ntests: []\n",
		promtoolPath: `#!/bin/sh
if [ "$1" = "--version" ]; then echo "promtool test-double"; exit 0; fi
if [ "$1" = "test" ] && [ "$2" = "--junit" ]; then
  printf '<testsuites><testsuite tests="1" failures="1" errors="0"><testcase name="alert"><failure>caught</failure></testcase></testsuite></testsuites>' > "$3"
  exit 1
fi
exit 0
`,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(promtoolPath, 0o700); err != nil {
		t.Fatal(err)
	}

	completed := 0
	progressTotal := 0
	report, err := Run(context.Background(), Options{
		RuleFiles: []string{rulePath}, TestFiles: []string{testPath}, Promtool: promtoolPath,
		Workers: 2, Timeout: 15 * time.Second, Threshold: 100, Limit: 2,
		Progress: func(done, total int, _ domain.Result) {
			completed = done
			progressTotal = total
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed != 2 || progressTotal != 2 || report.Summary.Total != 2 || report.Summary.Killed != 2 || !report.Summary.PassedGate {
		t.Fatalf("unexpected report: completed=%d total=%d summary=%#v", completed, progressTotal, report.Summary)
	}
}
