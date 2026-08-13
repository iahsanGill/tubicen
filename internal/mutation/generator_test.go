package mutation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iahsanGill/tubicen/internal/rules"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestGenerateProducesValidDeterministicMutants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yml")
	data := `groups:
- name: api
  rules:
  - alert: HighErrorRate
    expr: sum by (service) (rate(http_requests_total{status=~"5.."}[5m])) > 0.05
    for: 10m
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := rules.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	first, err := Generate(f)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 10 {
		t.Fatalf("generated %d mutants, want at least 10", len(first))
	}
	if len(first) != len(second) {
		t.Fatalf("non-deterministic count: %d != %d", len(first), len(second))
	}

	operators := map[string]bool{}
	for i, mutant := range first {
		if mutant.ID != second[i].ID {
			t.Fatalf("non-deterministic ID at %d: %s != %s", i, mutant.ID, second[i].ID)
		}
		operators[mutant.Operator] = true
		if mutant.Expression != "" {
			if _, err := parser.NewParser(parser.Options{}).ParseExpr(mutant.Expression); err != nil {
				t.Fatalf("invalid mutant %s: %v\n%s", mutant.ID, err, mutant.Expression)
			}
		}
	}

	for _, expected := range []string{
		"comparison.replace", "threshold.scale-up", "range.expand",
		"aggregation.replace", "selector.negate", "function.replace", "for.remove",
	} {
		if !operators[expected] {
			t.Errorf("missing operator %q", expected)
		}
	}
}

func TestGenerateAddsForMutationWhenDelayIsAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yml")
	if err := os.WriteFile(path, []byte(`groups:
- name: node
  rules:
  - alert: InstanceDown
    expr: up == 0
`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := rules.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mutants, err := Generate(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutant := range mutants {
		if mutant.Operator == "for.add" && mutant.For == "5m" {
			return
		}
	}
	t.Fatal("missing for.add mutant")
}

func TestThresholdOperatorNamesZeroShifts(t *testing.T) {
	t.Parallel()
	if got := thresholdOperator(0, -1); got != "threshold.shift-down" {
		t.Fatalf("negative shift operator = %q", got)
	}
	if got := thresholdOperator(0, 1); got != "threshold.shift-up" {
		t.Fatalf("positive shift operator = %q", got)
	}
}

func TestMutationIDsDistinguishSameNamedFiles(t *testing.T) {
	t.Parallel()
	alert := rules.Alert{Group: "api", Name: "Down"}
	first := newMutation(filepath.Join("cluster-a", "alerts.yml"), alert, "comparison.replace", "replace", "==", "!=")
	second := newMutation(filepath.Join("cluster-b", "alerts.yml"), alert, "comparison.replace", "replace", "==", "!=")
	if first.ID == second.ID {
		t.Fatalf("mutation IDs collide across different rule paths: %s", first.ID)
	}
}
