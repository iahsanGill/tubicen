package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/iahsanGill/tubicen/internal/campaign"
	"github.com/iahsanGill/tubicen/internal/domain"
	"github.com/iahsanGill/tubicen/internal/mutation"
	"github.com/iahsanGill/tubicen/internal/report"
	"github.com/iahsanGill/tubicen/internal/rules"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRootUsage(stderr)
		return 2
	}

	switch args[0] {
	case "run":
		return runCampaign(ctx, args[1:], stdout, stderr)
	case "list":
		return listMutations(args[1:], stdout, stderr)
	case "version", "--version", "-version":
		fmt.Fprintf(stdout, "tubicen %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "help", "--help", "-h":
		printRootUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printRootUsage(stderr)
		return 2
	}
}

func runCampaign(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tubicen run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printRunUsage(stderr, flags) }

	var ruleFiles, testFiles stringList
	flags.Var(&ruleFiles, "rules", "Prometheus rule file (repeatable; required)")
	flags.Var(&testFiles, "tests", "promtool rule test file (repeatable; required)")
	promtool := flags.String("promtool", "promtool", "promtool executable")
	workers := flags.Int("workers", 0, "parallel mutants (0 uses a safe CPU-based default)")
	timeout := flags.Duration("timeout", 30*time.Second, "timeout for each promtool command")
	threshold := flags.Float64("threshold", 80, "minimum mutation score required, from 0 to 100")
	only := flags.String("only", "", "comma-separated operator families to include")
	skip := flags.String("skip", "", "comma-separated operator families to exclude")
	limit := flags.Int("limit", 0, "maximum mutants to run (0 means all)")
	survivors := flags.String("survivors", "", "directory in which to save surviving rule files")
	jsonPath := flags.String("json", "", "write the machine-readable JSON report")
	junitPath := flags.String("junit", "", "write a JUnit XML report")
	sarifPath := flags.String("sarif", "", "write a SARIF 2.1 report")
	htmlPath := flags.String("html", "", "write a standalone HTML report")
	quiet := flags.Bool("quiet", false, "hide per-mutant progress")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if len(ruleFiles) == 0 || len(testFiles) == 0 {
		fmt.Fprintln(stderr, "both --rules and --tests are required")
		return 2
	}

	options := campaign.Options{
		RuleFiles:     ruleFiles,
		TestFiles:     testFiles,
		Promtool:      *promtool,
		Workers:       *workers,
		Timeout:       *timeout,
		Threshold:     *threshold,
		OnlyOperators: splitList(*only),
		SkipOperators: splitList(*skip),
		Limit:         *limit,
		SurvivorsDir:  *survivors,
		ToolVersion:   version,
	}
	if !*quiet {
		options.Progress = func(completed, total int, result domain.Result) {
			fmt.Fprintf(stderr, "[%d/%d] %-8s %s/%s %s\n", completed, total, strings.ToUpper(string(result.Status)), result.Mutation.Group, result.Mutation.Alert, result.Mutation.Operator)
		}
	}

	result, err := campaign.Run(ctx, options)
	if err != nil {
		fmt.Fprintf(stderr, "tubicen: %v\n", err)
		return 2
	}
	if err := report.Terminal(stdout, result); err != nil {
		fmt.Fprintf(stderr, "write terminal report: %v\n", err)
		return 2
	}
	reports := []struct {
		path   string
		render func(io.Writer, domain.Report) error
	}{
		{path: *jsonPath, render: report.JSON},
		{path: *junitPath, render: report.JUnit},
		{path: *sarifPath, render: report.SARIF},
		{path: *htmlPath, render: report.HTML},
	}
	for _, output := range reports {
		if err := report.WriteFile(output.path, result, output.render); err != nil {
			fmt.Fprintf(stderr, "write report %q: %v\n", output.path, err)
			return 2
		}
		if output.path != "" {
			fmt.Fprintf(stderr, "wrote %s\n", output.path)
		}
	}
	if !result.Summary.PassedGate {
		return 1
	}
	return 0
}

func listMutations(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tubicen list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printListUsage(stderr, flags) }
	var ruleFiles stringList
	flags.Var(&ruleFiles, "rules", "Prometheus rule file (repeatable; required)")
	asJSON := flags.Bool("json", false, "emit JSON instead of a table")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if len(ruleFiles) == 0 {
		fmt.Fprintln(stderr, "--rules is required")
		return 2
	}

	var generated []domain.Mutation
	for _, path := range ruleFiles {
		file, err := rules.Load(path)
		if err != nil {
			fmt.Fprintf(stderr, "tubicen: %v\n", err)
			return 2
		}
		mutants, err := mutation.Generate(file)
		if err != nil {
			fmt.Fprintf(stderr, "tubicen: %v\n", err)
			return 2
		}
		generated = append(generated, mutants...)
	}
	sort.Slice(generated, func(i, j int) bool {
		if generated[i].RuleFile != generated[j].RuleFile {
			return generated[i].RuleFile < generated[j].RuleFile
		}
		if generated[i].Alert != generated[j].Alert {
			return generated[i].Alert < generated[j].Alert
		}
		if generated[i].Operator != generated[j].Operator {
			return generated[i].Operator < generated[j].Operator
		}
		return generated[i].ID < generated[j].ID
	})

	if *asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(generated); err != nil {
			fmt.Fprintf(stderr, "write mutations: %v\n", err)
			return 2
		}
		return 0
	}
	fmt.Fprintf(stdout, "%-12s  %-24s  %-24s  %s\n", "ID", "ALERT", "OPERATOR", "CHANGE")
	for _, mutant := range generated {
		change := mutant.Original + " -> " + mutant.Replacement
		fmt.Fprintf(stdout, "%-12s  %-24s  %-24s  %s\n", mutant.ID, truncate(mutant.Alert, 24), mutant.Operator, change)
	}
	fmt.Fprintf(stdout, "\n%d mutants across %d rule file(s)\n", len(generated), len(ruleFiles))
	return 0
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, `Tubicen — mutation testing for Prometheus alert rules.

Usage:
  tubicen run      execute a mutation campaign and enforce a score
  tubicen list     preview mutations without running promtool
  tubicen version  print build information

Run "tubicen <command> --help" for command flags.`)
}

func printRunUsage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: tubicen run --rules alerts.yml --tests alerts_test.yml [flags]")
	fmt.Fprintln(w)
	flags.PrintDefaults()
}

func printListUsage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: tubicen list --rules alerts.yml [--rules more.yml]")
	fmt.Fprintln(w)
	flags.PrintDefaults()
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	for _, path := range splitList(value) {
		if path != "" {
			*values = append(*values, filepath.Clean(path))
		}
	}
	return nil
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func truncate(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}
