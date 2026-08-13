package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
)

func sampleReport() domain.Report {
	return domain.Report{
		SchemaVersion: "1.0", ToolVersion: "test", Promtool: "promtool test",
		StartedAt: time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC), Duration: "1.5s",
		Summary: domain.Summary{Total: 2, Killed: 1, Survived: 1, Score: 50, Threshold: 80},
		Results: []domain.Result{
			{Status: domain.StatusKilled, Duration: time.Second, Mutation: domain.Mutation{ID: "one", Alert: "Down", Group: "api", Operator: "comparison.replace", RuleFile: "rules.yml", Line: 4}},
			{Status: domain.StatusSurvived, Duration: 500 * time.Millisecond, Mutation: domain.Mutation{ID: "two", Alert: "Slow", Group: "api", Operator: "threshold.scale-up", Description: "raise threshold", Original: "5", Replacement: "50", RuleFile: "rules.yml", Line: 8}},
		},
	}
}

func TestRenderers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		render func(interfaceWriter, domain.Report) error
		want   string
	}{
		{name: "json", render: func(w interfaceWriter, r domain.Report) error { return JSON(w, r) }, want: `"schema_version": "1.0"`},
		{name: "junit", render: func(w interfaceWriter, r domain.Report) error { return JUnit(w, r) }, want: `<failure message="mutant survived`},
		{name: "sarif", render: func(w interfaceWriter, r domain.Report) error { return SARIF(w, r) }, want: `"version": "2.1.0"`},
		{name: "html", render: func(w interfaceWriter, r domain.Report) error { return HTML(w, r) }, want: `Alert rule test report`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			if err := test.render(&output, sampleReport()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("output does not contain %q:\n%s", test.want, output.String())
			}
			if test.name == "json" && !json.Valid(output.Bytes()) {
				t.Fatal("invalid JSON")
			}
		})
	}
}

type interfaceWriter interface {
	Write([]byte) (int, error)
}

func TestTerminalShowsSurvivor(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := Terminal(&output, sampleReport()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "[NOT CAUGHT]") || !strings.Contains(output.String(), "50.0%") {
		t.Fatalf("unexpected terminal report:\n%s", output.String())
	}
}
