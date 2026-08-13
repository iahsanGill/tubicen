package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
)

// Terminal writes a compact human-readable test report.
func Terminal(w io.Writer, report domain.Report) error {
	summary := report.Summary
	verdict := "PASS"
	if !summary.PassedGate {
		verdict = "FAIL"
	}

	if _, err := fmt.Fprintf(w, "\nTubicen alert rule test report\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Score       %6.1f%%  %s\n", summary.Score, scoreBar(summary.Score)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Gate        %6.1f%%  %s\n", summary.Threshold, verdict); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Changes     %6d  %d caught, %d not caught, %d errors, %d timeouts\n", summary.Total, summary.Killed, summary.Survived, summary.Errors, summary.Timeouts); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Duration    %6s\n", report.Duration); err != nil {
		return err
	}

	problemCount := summary.Survived + summary.Errors + summary.Timeouts
	if problemCount == 0 {
		_, err := fmt.Fprintln(w, "\nAll rule changes were caught by the test suite.")
		return err
	}
	if _, err := fmt.Fprint(w, "\nChanges not caught and test errors:\n\n"); err != nil {
		return err
	}
	for _, result := range report.Results {
		if result.Status == domain.StatusKilled {
			continue
		}
		mutation := result.Mutation
		status := strings.ToUpper(string(result.Status))
		if result.Status == domain.StatusSurvived {
			status = "NOT CAUGHT"
		}
		if _, err := fmt.Fprintf(w, "  [%s] %s/%s  %s  (%s:%d)\n", status, mutation.Group, mutation.Alert, ChangeType(mutation.Operator), filepath.Base(mutation.RuleFile), mutation.Line); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "      %s\n", PlainChange(mutation)); err != nil {
			return err
		}
		if mutation.Original != "" || mutation.Replacement != "" {
			if _, err := fmt.Fprintf(w, "      %s  ->  %s\n", mutation.Original, mutation.Replacement); err != nil {
				return err
			}
		}
		if result.Output != "" && result.Status != domain.StatusSurvived {
			if _, err := fmt.Fprintf(w, "      %s\n", firstLine(result.Output)); err != nil {
				return err
			}
		}
	}
	return nil
}

// JSON writes the stable report schema as indented JSON.
func JSON(w io.Writer, report domain.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// JUnit writes campaign results as a JUnit test suite. Killed mutants are
// successful test cases; survivors are failures; execution problems are errors.
func JUnit(w io.Writer, report domain.Report) error {
	suite := junitSuite{
		Name:      "tubicen",
		Tests:     report.Summary.Total,
		Failures:  report.Summary.Survived,
		Errors:    report.Summary.Errors + report.Summary.Timeouts,
		Time:      seconds(report.Duration),
		Timestamp: report.StartedAt.Format(time.RFC3339),
	}
	for _, result := range report.Results {
		mutation := result.Mutation
		item := junitCase{
			Name:      mutation.ID + " " + mutation.Operator,
			Classname: mutation.Group + "." + mutation.Alert,
			Time:      fmt.Sprintf("%.3f", result.Duration.Seconds()),
		}
		switch result.Status {
		case domain.StatusSurvived:
			item.Failure = &junitFailure{
				Message: "alert rule change not caught: " + PlainChange(mutation),
				Body:    mutation.Original + " -> " + mutation.Replacement,
			}
		case domain.StatusError, domain.StatusTimeout:
			item.Error = &junitFailure{Message: string(result.Status), Body: result.Output}
		}
		suite.Cases = append(suite.Cases, item)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")
	return encoder.Encode(suite)
}

// SARIF writes surviving mutants and execution problems for code-scanning UIs.
func SARIF(w io.Writer, report domain.Report) error {
	document := sarifDocument{Version: "2.1.0", Schema: "https://json.schemastore.org/sarif-2.1.0.json"}
	run := sarifRun{Tool: sarifTool{Driver: sarifDriver{Name: "Tubicen", Version: report.ToolVersion, InformationURI: "https://github.com/iahsanGill/tubicen"}}}
	knownRules := map[string]bool{}
	for _, result := range report.Results {
		if result.Status == domain.StatusKilled {
			continue
		}
		mutation := result.Mutation
		if !knownRules[mutation.Operator] {
			run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
				ID:               mutation.Operator,
				ShortDescription: sarifMessage{Text: "Alert rule change was not caught by tests"},
				HelpURI:          "https://github.com/iahsanGill/tubicen#changes-currently-tested",
			})
			knownRules[mutation.Operator] = true
		}
		level := "warning"
		message := fmt.Sprintf("Tests did not catch this rule change: %s (%s -> %s)", PlainChange(mutation), mutation.Original, mutation.Replacement)
		if result.Status == domain.StatusError || result.Status == domain.StatusTimeout {
			level = "error"
			message = fmt.Sprintf("Mutation execution %s: %s", result.Status, firstLine(result.Output))
		}
		run.Results = append(run.Results, sarifResult{
			RuleID:  mutation.Operator,
			Level:   level,
			Message: sarifMessage{Text: message},
			Locations: []sarifLocation{{PhysicalLocation: sarifPhysicalLocation{
				ArtifactLocation: sarifArtifactLocation{URI: artifactURI(mutation.RuleFile)},
				Region:           sarifRegion{StartLine: max(mutation.Line, 1)},
			}}},
		})
	}
	document.Runs = []sarifRun{run}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

// HTML writes a self-contained test report.
func HTML(w io.Writer, report domain.Report) error {
	functions := template.FuncMap{
		"upper": strings.ToUpper,
		"base":  filepath.Base,
		"first": firstLine,
		"sum":   func(left, right int) int { return left + right },
		"changeType": func(mutation domain.Mutation) string {
			return ChangeType(mutation.Operator)
		},
		"plainChange": PlainChange,
		"class":       func(status domain.Status) string { return string(status) },
		"fmtDuration": func(value time.Duration) string {
			return value.Round(time.Millisecond).String()
		},
	}
	tmpl, err := template.New("report").Funcs(functions).Parse(htmlTemplate)
	if err != nil {
		return err
	}
	return tmpl.Execute(w, report)
}

// WriteFile creates a report file using the supplied renderer.
func WriteFile(path string, report domain.Report, render func(io.Writer, domain.Report) error) error {
	if path == "" {
		return nil
	}
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return render(file, report)
}

// GitHubAnnotations writes workflow commands that attach failed checks to the
// changed rule lines in a pull request.
func GitHubAnnotations(w io.Writer, report domain.Report) error {
	for _, result := range report.Results {
		if result.Status == domain.StatusKilled {
			continue
		}
		mutation := result.Mutation
		title := "Alert rule change not caught"
		message := PlainChange(mutation)
		if result.Status == domain.StatusError || result.Status == domain.StatusTimeout {
			title = "Alert rule check failed"
			message = firstLine(result.Output)
		}
		if _, err := fmt.Fprintf(
			w,
			"::error file=%s,line=%d,title=%s::%s\n",
			workflowProperty(artifactURI(mutation.RuleFile)),
			max(mutation.Line, 1),
			workflowProperty(title),
			workflowMessage(message),
		); err != nil {
			return err
		}
	}
	return nil
}

func scoreBar(score float64) string {
	const width = 20
	filled := int(score / 100 * width)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	if len(value) > 180 {
		value = value[:177] + "..."
	}
	return value
}

func artifactURI(path string) string {
	workingDirectory, err := os.Getwd()
	if err == nil {
		if relative, relErr := filepath.Rel(workingDirectory, path); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path = relative
		}
	}
	return filepath.ToSlash(path)
}

// ChangeType gives an operator family a production-facing label.
func ChangeType(operator string) string {
	switch {
	case strings.HasPrefix(operator, "aggregation"):
		return "How replicas are checked"
	case strings.HasPrefix(operator, "comparison"):
		return "Alert condition"
	case strings.HasPrefix(operator, "threshold"):
		return "Alert threshold"
	case strings.HasPrefix(operator, "for."):
		return "Time before alerting"
	case strings.HasPrefix(operator, "range."):
		return "Metric time window"
	case strings.HasPrefix(operator, "selector"):
		return "Metrics included"
	case strings.HasPrefix(operator, "function"):
		return "Metric calculation"
	case strings.HasPrefix(operator, "logical"):
		return "Combined conditions"
	default:
		return "Rule behavior"
	}
}

// PlainChange explains a generated change without mutation-testing jargon.
func PlainChange(mutation domain.Mutation) string {
	switch mutation.Operator {
	case "aggregation.replace":
		if mutation.Original == "min" && mutation.Replacement == "max" {
			return "Check the healthiest replica instead of the least healthy replica"
		}
		if mutation.Original == "max" && mutation.Replacement == "min" {
			return "Check the least healthy replica instead of the healthiest replica"
		}
		return fmt.Sprintf("Change the group calculation from %s to %s", mutation.Original, mutation.Replacement)
	case "comparison.replace":
		return fmt.Sprintf("Change the alert condition from %s to %s", mutation.Original, mutation.Replacement)
	case "for.remove":
		return "Remove the wait before the alert fires"
	case "for.add":
		return fmt.Sprintf("Add a %s wait before the alert fires", mutation.Replacement)
	case "for.contract", "for.expand":
		return fmt.Sprintf("Change the wait before alerting from %s to %s", mutation.Original, mutation.Replacement)
	case "range.contract", "range.expand":
		return fmt.Sprintf("Change the metric time window from %s to %s", mutation.Original, mutation.Replacement)
	case "threshold.scale-up", "threshold.scale-down", "threshold.shift-up", "threshold.shift-down":
		return fmt.Sprintf("Change the alert threshold from %s to %s", mutation.Original, mutation.Replacement)
	case "selector.negate":
		return fmt.Sprintf("Change which metrics are included: %s becomes %s", mutation.Original, mutation.Replacement)
	case "function.replace":
		return fmt.Sprintf("Change the metric calculation from %s to %s", mutation.Original, mutation.Replacement)
	case "logical.replace":
		return fmt.Sprintf("Change how conditions are combined from %s to %s", mutation.Original, mutation.Replacement)
	default:
		return mutation.Description
	}
}

func workflowProperty(value string) string {
	replacer := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
	return replacer.Replace(value)
}

func workflowMessage(value string) string {
	replacer := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	return replacer.Replace(value)
}

func seconds(duration string) string {
	parsed, err := time.ParseDuration(duration)
	if err != nil {
		return "0"
	}
	return fmt.Sprintf("%.3f", parsed.Seconds())
}

type junitSuite struct {
	XMLName   xml.Name    `xml:"testsuite"`
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitFailure `xml:"error,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type sarifDocument struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules,omitempty"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
	HelpURI          string       `json:"helpUri,omitempty"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

const htmlTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Tubicen alert rule test report</title>
  <style>
    :root { color-scheme:light; --ink:#20231f; --ink-2:#343a34; --paper:#f0eee8; --sheet:#f9f8f4; --line:#c9c7bd; --line-dark:#85877f; --quiet:#6d716a; --oxide:#9d3f2b; --green:#2e6048; --brass:#a87c37; }
    * { box-sizing:border-box; }
    html { background:var(--paper); }
    body { margin:0; color:var(--ink); background:var(--paper); font:14px/1.45 Arial,Helvetica,sans-serif; }
    .top-rule { height:7px; background:linear-gradient(90deg,var(--oxide) 0 18%,var(--ink) 18% 100%); }
    main { width:min(1280px,calc(100% - 48px)); margin:30px auto 64px; }
    .masthead { display:grid; grid-template-columns:1fr auto; align-items:end; padding:0 0 19px; border-bottom:2px solid var(--ink); }
    .brand { display:flex; align-items:center; gap:14px; }
    .sigil { display:grid; place-items:center; width:42px; height:42px; color:var(--sheet); background:var(--ink); font:700 18px/1 Georgia,serif; }
    .brand-name { margin:0; font:700 28px/.95 Georgia,"Times New Roman",serif; letter-spacing:.03em; }
    .brand-line { margin:5px 0 0; color:var(--quiet); font:700 10px/1.2 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.14em; text-transform:uppercase; }
    .document-id { text-align:right; color:var(--quiet); font:11px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace; text-transform:uppercase; }
    .document-id strong { color:var(--ink); }
    .brief { display:grid; grid-template-columns:minmax(0,1fr) 218px; border:1px solid var(--line-dark); border-top:0; background:var(--sheet); }
    .brief-main { padding:32px 34px 28px; }
    .section-tag { margin:0 0 12px; color:var(--oxide); font:700 10px/1 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.16em; text-transform:uppercase; }
    h1 { max-width:730px; margin:0; font:400 35px/1.12 Georgia,"Times New Roman",serif; letter-spacing:-.025em; }
    .lede { max-width:730px; margin:14px 0 0; color:var(--quiet); font-size:14px; }
    .gate { display:flex; flex-direction:column; justify-content:space-between; min-height:188px; padding:24px; color:var(--sheet); background:var(--ink); border-left:1px solid var(--line-dark); }
    .gate-label { color:#b8b9b2; font:700 10px/1 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.15em; text-transform:uppercase; }
    .gate-value { font:700 28px/1 Georgia,"Times New Roman",serif; }
    .gate-value.pass { color:#9bc1a7; } .gate-value.fail { color:#e39b87; }
    .gate-note { color:#b8b9b2; font:11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace; }
    .scoreboard { display:grid; grid-template-columns:1.65fr repeat(4,1fr); margin-top:22px; border:1px solid var(--line-dark); background:var(--sheet); }
    .score-cell { min-height:116px; padding:19px 20px; border-right:1px solid var(--line); }
    .score-cell:last-child { border-right:0; }
    .score-cell label { display:block; color:var(--quiet); font:700 10px/1 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.12em; text-transform:uppercase; }
    .score-cell strong { display:block; margin-top:13px; font:400 32px/1 Georgia,"Times New Roman",serif; font-variant-numeric:tabular-nums; }
    .score-cell.primary strong { margin-top:8px; font-size:44px; }
    .score-cell.primary strong small { font-size:20px; }
	.scale { height:4px; margin-top:12px; background:var(--line); }
	.scale i { display:block; height:100%; background:var(--green); }
    .score-cell .zero { color:var(--green); }
    .workspace { margin-top:30px; border-top:2px solid var(--ink); }
    .workspace-head { display:grid; grid-template-columns:1fr auto; align-items:end; gap:24px; padding:18px 0 13px; border-bottom:1px solid var(--line-dark); }
    .workspace-title { margin:0; font:700 17px/1.1 Georgia,"Times New Roman",serif; }
    .filters { display:flex; gap:22px; }
    button { position:relative; padding:0 0 5px; border:0; color:var(--quiet); background:transparent; cursor:pointer; font:700 11px/1 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.04em; text-transform:uppercase; }
    button::after { position:absolute; left:0; right:100%; bottom:0; height:2px; content:""; background:var(--oxide); transition:right .15s ease; }
    button:hover { color:var(--ink); } button.active { color:var(--ink); } button.active::after { right:0; }
    .table-wrap { overflow-x:auto; border-bottom:1px solid var(--line-dark); }
    table { width:100%; border-collapse:collapse; background:var(--sheet); }
    th { padding:11px 14px; color:var(--quiet); background:#e7e5de; border-bottom:1px solid var(--line-dark); text-align:left; font:700 9px/1 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.14em; text-transform:uppercase; }
    th:first-child,td:first-child { padding-left:18px; }
    th:last-child,td:last-child { padding-right:18px; text-align:right; }
    td { padding:15px 14px; border-bottom:1px solid var(--line); vertical-align:top; }
    tr:last-child td { border-bottom:0; }
    tr:hover td { background:#f4f2ec; }
    .status { display:inline-flex; align-items:center; gap:8px; font:700 10px/1 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.07em; }
    .status::before { width:7px; height:7px; content:""; background:currentColor; }
    .killed { color:var(--green); } .survived { color:var(--oxide); } .error,.timeout { color:var(--brass); }
    .alert { font-weight:700; }
    .secondary { margin-top:5px; color:var(--quiet); font-size:12px; }
    .technical { margin-top:6px; color:#8b8d87; font:9px/1.3 ui-monospace,SFMono-Regular,Menlo,monospace; }
    code { padding:2px 4px; color:var(--ink-2); background:#e9e8e2; font:11px/1.35 ui-monospace,SFMono-Regular,Menlo,monospace; }
    .operator { display:inline-block; margin-bottom:2px; }
    .change { white-space:nowrap; }
    .arrow { padding:0 6px; color:var(--oxide); font-weight:700; }
    .duration { color:var(--quiet); font:11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace; font-variant-numeric:tabular-nums; }
    footer { display:grid; grid-template-columns:1fr auto; gap:24px; padding:15px 2px 0; color:var(--quiet); font:10px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; text-transform:uppercase; }
    footer span:last-child { text-align:right; }
    [hidden] { display:none; }
    @media (max-width:850px) { main { width:min(100% - 24px,1280px); margin-top:18px; } .brief { grid-template-columns:1fr; } .gate { min-height:130px; border-left:0; border-top:1px solid var(--line-dark); } .scoreboard { grid-template-columns:1fr 1fr; } .score-cell { border-bottom:1px solid var(--line); } .score-cell.primary { grid-column:span 2; } .workspace-head { display:block; } .filters { margin-top:18px; overflow-x:auto; } }
    @media (max-width:520px) { .document-id { display:none; } .brief-main { padding:25px 22px; } h1 { font-size:29px; } .scoreboard { grid-template-columns:1fr; } .score-cell.primary { grid-column:auto; } .masthead { grid-template-columns:1fr; } footer { grid-template-columns:1fr; } footer span:last-child { text-align:left; } }
  </style>
</head>
<body>
<div class="top-rule"></div>
<main>
  <header class="masthead">
    <div class="brand"><div class="sigil">T</div><div><p class="brand-name">Tubicen</p><p class="brand-line">Prometheus alert rule test</p></div></div>
    <div class="document-id"><strong>Test run</strong><br>{{.StartedAt.Format "2006-01-02 / 15:04 UTC"}}</div>
  </header>

  <section class="brief">
    <div class="brief-main">
      <p class="section-tag">Prometheus alert rule tests</p>
      <h1>Alert rule test report</h1>
      <p class="lede">Tubicen changed {{len .RuleFiles}} Prometheus rule file{{if ne (len .RuleFiles) 1}}s{{end}}, one change at a time, and reran {{len .TestFiles}} test file{{if ne (len .TestFiles) 1}}s{{end}}. The results show which rule changes the current tests caught and which ones they missed.</p>
    </div>
    <aside class="gate">
      <span class="gate-label">Quality gate / {{printf "%.1f" .Summary.Threshold}}%</span>
      <strong class="gate-value {{if .Summary.PassedGate}}pass{{else}}fail{{end}}">{{if .Summary.PassedGate}}PASS{{else}}FAIL{{end}}</strong>
      <span class="gate-note">{{if .Summary.PassedGate}}Required score met. No test errors.{{else}}Required score missed or test errors found.{{end}}</span>
    </aside>
  </section>

  <section class="scoreboard" aria-label="Test summary">
    <div class="score-cell primary"><label>Test score</label><strong>{{printf "%.1f" .Summary.Score}}<small>%</small></strong><div class="scale" aria-hidden="true"><i style="width:{{printf "%.1f" .Summary.Score}}%"></i></div></div>
    <div class="score-cell"><label>Changes tested</label><strong>{{.Summary.Total}}</strong></div>
    <div class="score-cell"><label>Caught by tests</label><strong>{{.Summary.Killed}}</strong></div>
    <div class="score-cell"><label>Not caught</label><strong class="{{if eq .Summary.Survived 0}}zero{{end}}">{{.Summary.Survived}}</strong></div>
    <div class="score-cell"><label>Test errors</label><strong>{{sum .Summary.Errors .Summary.Timeouts}}</strong></div>
  </section>

  <section class="workspace">
    <div class="workspace-head">
      <div><p class="section-tag">Test results</p><h2 class="workspace-title">Changes and test outcomes</h2></div>
      <nav class="filters" aria-label="Filter test results">
        <button class="active" data-filter="all" aria-pressed="true">All / {{.Summary.Total}}</button>
        <button data-filter="survived" aria-pressed="false">Not caught / {{.Summary.Survived}}</button>
        <button data-filter="killed" aria-pressed="false">Caught / {{.Summary.Killed}}</button>
        <button data-filter="problem" aria-pressed="false">Errors / {{sum .Summary.Errors .Summary.Timeouts}}</button>
      </nav>
    </div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Result</th><th>Alert / source</th><th>Change tested</th><th>Before → after</th><th>Time</th></tr></thead>
        <tbody>
        {{range .Results}}
          <tr data-status="{{class .Status}}">
            <td><span class="status {{class .Status}}">{{if eq (class .Status) "killed"}}CAUGHT{{else if eq (class .Status) "survived"}}NOT CAUGHT{{else}}{{upper (class .Status)}}{{end}}</span></td>
            <td><div class="alert">{{.Mutation.Alert}}</div><div class="secondary">{{.Mutation.Group}} / {{base .Mutation.RuleFile}}:{{.Mutation.Line}}</div></td>
            <td><div class="alert">{{changeType .Mutation}}</div><div class="secondary">{{plainChange .Mutation}}</div><div class="technical">{{.Mutation.Operator}}</div></td>
            <td class="change"><code>{{.Mutation.Original}}</code><span class="arrow">→</span><code>{{.Mutation.Replacement}}</code></td>
            <td class="duration">{{fmtDuration .Duration}}</td>
          </tr>
        {{end}}
        </tbody>
      </table>
    </div>
  </section>

  <footer><span>Tubicen / {{.ToolVersion}} / schema {{.SchemaVersion}}</span><span>{{first .Promtool}} / completed in {{.Duration}}</span></footer>
</main>
<script>
for (const button of document.querySelectorAll('button[data-filter]')) button.addEventListener('click', () => {
  document.querySelectorAll('button[data-filter]').forEach(item => { item.classList.remove('active'); item.setAttribute('aria-pressed', 'false'); });
  button.classList.add('active'); button.setAttribute('aria-pressed', 'true');
  const filter = button.dataset.filter;
  document.querySelectorAll('tbody tr').forEach(row => {
    const status = row.dataset.status;
    row.hidden = !(filter === 'all' || status === filter || (filter === 'problem' && (status === 'error' || status === 'timeout')));
  });
});
</script>
</body>
</html>`
