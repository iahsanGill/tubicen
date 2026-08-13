package campaign

import (
	"testing"

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
