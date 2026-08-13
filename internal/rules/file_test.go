package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iahsanGill/tubicen/internal/domain"
)

func TestLoadAndRender(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yml")
	input := `groups:
  - name: api
    rules:
      - alert: HighErrorRate
        expr: rate(http_requests_total{code=~"5.."}[5m]) > 0.05
        for: 10m
        labels:
          severity: page
      - record: api:requests:rate5m
        expr: rate(http_requests_total[5m])
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(f.Alerts))
	}
	if f.Alerts[0].Name != "HighErrorRate" || f.Alerts[0].For != "10m" {
		t.Fatalf("unexpected alert: %#v", f.Alerts[0])
	}

	out, err := f.Render(domain.Mutation{
		GroupIndex: 0,
		RuleIndex:  0,
		Expression: `rate(http_requests_total{code=~"5.."}[5m]) > 0.5`,
		RemoveFor:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "> 0.5") {
		t.Fatalf("mutated expression missing:\n%s", text)
	}
	if strings.Contains(text, "for: 10m") {
		t.Fatalf("for field was not removed:\n%s", text)
	}
	if !strings.Contains(text, "severity: page") {
		t.Fatalf("unrelated content was lost:\n%s", text)
	}
}
