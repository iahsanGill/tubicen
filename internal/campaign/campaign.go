package campaign

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
	"github.com/iahsanGill/tubicen/internal/mutation"
	"github.com/iahsanGill/tubicen/internal/promtool"
	"github.com/iahsanGill/tubicen/internal/rules"
)

// Options configures a mutation campaign.
type Options struct {
	RuleFiles     []string
	TestFiles     []string
	Promtool      string
	Workers       int
	Timeout       time.Duration
	Threshold     float64
	OnlyOperators []string
	SkipOperators []string
	Limit         int
	SurvivorsDir  string
	ToolVersion   string
	Progress      func(completed, total int, result domain.Result)
}

type job struct {
	index    int
	file     *rules.File
	mutation domain.Mutation
}

// Run validates the baseline and executes every selected mutant.
func Run(ctx context.Context, options Options) (domain.Report, error) {
	started := time.Now().UTC()
	if len(options.RuleFiles) == 0 {
		return domain.Report{}, fmt.Errorf("at least one rule file is required")
	}
	if len(options.TestFiles) == 0 {
		return domain.Report{}, fmt.Errorf("at least one test file is required")
	}
	if options.Threshold < 0 || options.Threshold > 100 {
		return domain.Report{}, fmt.Errorf("threshold must be between 0 and 100")
	}
	if options.Workers <= 0 {
		options.Workers = min(runtime.NumCPU(), 8)
	}
	if options.ToolVersion == "" {
		options.ToolVersion = "dev"
	}

	ruleFiles, err := absolutePaths(options.RuleFiles)
	if err != nil {
		return domain.Report{}, err
	}
	testFiles, err := absolutePaths(options.TestFiles)
	if err != nil {
		return domain.Report{}, err
	}

	loaded := make(map[string]*rules.File, len(ruleFiles))
	var mutations []domain.Mutation
	for _, path := range ruleFiles {
		file, err := rules.Load(path)
		if err != nil {
			return domain.Report{}, err
		}
		loaded[file.Path] = file
		generated, err := mutation.Generate(file)
		if err != nil {
			return domain.Report{}, err
		}
		for _, mutant := range generated {
			if operatorSelected(mutant.Operator, options.OnlyOperators, options.SkipOperators) {
				mutations = append(mutations, mutant)
			}
		}
	}
	if options.Limit > 0 && len(mutations) > options.Limit {
		mutations = mutations[:options.Limit]
	}
	if len(mutations) == 0 {
		return domain.Report{}, fmt.Errorf("no mutants selected")
	}

	runner := promtool.NewRunner(options.Promtool, options.Timeout)
	promtoolVersion, err := runner.Version(ctx)
	if err != nil {
		return domain.Report{}, err
	}
	if err := runner.ValidateBaseline(ctx, ruleFiles, testFiles); err != nil {
		return domain.Report{}, err
	}

	results := make([]domain.Result, len(mutations))
	jobs := make(chan job)
	var workers sync.WaitGroup
	var progress sync.Mutex
	completed := 0
	workerCount := min(options.Workers, len(mutations))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				result := runner.Execute(ctx, item.file, item.mutation, testFiles, options.SurvivorsDir)
				results[item.index] = result
				if options.Progress != nil {
					progress.Lock()
					completed++
					options.Progress(completed, len(mutations), result)
					progress.Unlock()
				}
			}
		}()
	}

	for index, mutant := range mutations {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return domain.Report{}, ctx.Err()
		case jobs <- job{index: index, file: loaded[mutant.RuleFile], mutation: mutant}:
		}
	}
	close(jobs)
	workers.Wait()

	summary := summarize(results, options.Threshold)
	return domain.Report{
		SchemaVersion: "1.0",
		ToolVersion:   options.ToolVersion,
		Promtool:      promtoolVersion,
		StartedAt:     started,
		Duration:      time.Since(started).Round(time.Millisecond).String(),
		RuleFiles:     ruleFiles,
		TestFiles:     testFiles,
		Summary:       summary,
		Results:       results,
	}, nil
}

func summarize(results []domain.Result, threshold float64) domain.Summary {
	summary := domain.Summary{Total: len(results), Threshold: threshold}
	for _, result := range results {
		switch result.Status {
		case domain.StatusKilled:
			summary.Killed++
		case domain.StatusSurvived:
			summary.Survived++
		case domain.StatusTimeout:
			summary.Timeouts++
		default:
			summary.Errors++
		}
	}
	scorable := summary.Killed + summary.Survived
	if scorable > 0 {
		summary.Score = float64(summary.Killed) / float64(scorable) * 100
	}
	summary.PassedGate = summary.Score >= threshold && summary.Errors == 0 && summary.Timeouts == 0
	return summary
}

func operatorSelected(operator string, only, skip []string) bool {
	if matchesAny(operator, skip) {
		return false
	}
	return len(only) == 0 || matchesAny(operator, only)
}

func matchesAny(operator string, filters []string) bool {
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter != "" && (operator == filter || strings.HasPrefix(operator, filter+".")) {
			return true
		}
	}
	return false
}

func absolutePaths(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", path, err)
		}
		if _, ok := seen[absolute]; ok {
			continue
		}
		seen[absolute] = struct{}{}
		result = append(result, absolute)
	}
	sort.Strings(result)
	return result, nil
}
