package promtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
	"github.com/iahsanGill/tubicen/internal/rules"
	"gopkg.in/yaml.v3"
)

// Runner executes Prometheus' reference rule checker and unit-test runner.
type Runner struct {
	Binary  string
	Timeout time.Duration
}

// NewRunner returns a runner with production defaults.
func NewRunner(binary string, timeout time.Duration) Runner {
	if binary == "" {
		binary = "promtool"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return Runner{Binary: binary, Timeout: timeout}
}

// Version verifies the promtool binary and returns its version text.
func (r Runner) Version(ctx context.Context) (string, error) {
	output, err := r.command(ctx, "--version")
	if err != nil {
		return "", fmt.Errorf("run %s --version: %w", r.Binary, err)
	}
	return strings.TrimSpace(output), nil
}

// ValidateBaseline requires every rule file to be valid and every original test to pass.
func (r Runner) ValidateBaseline(ctx context.Context, ruleFiles, testFiles []string) error {
	for _, path := range ruleFiles {
		if output, err := r.command(ctx, "check", "rules", path); err != nil {
			return fmt.Errorf("baseline rule validation failed for %q: %w\n%s", path, err, output)
		}
	}
	for _, path := range testFiles {
		if output, err := r.command(ctx, "test", "rules", path); err != nil {
			return fmt.Errorf("baseline test failed for %q: %w\n%s", path, err, output)
		}
	}
	return nil
}

// Execute renders and tests a single mutant. A test failure kills the mutant;
// malformed mutated rules are reported as infrastructure errors instead.
func (r Runner) Execute(ctx context.Context, file *rules.File, mutation domain.Mutation, testFiles []string, survivorsDir string) domain.Result {
	started := time.Now()
	result := domain.Result{Mutation: mutation}

	workspace, err := os.MkdirTemp("", "tubicen-mutant-*")
	if err != nil {
		result.Status = domain.StatusError
		result.Output = err.Error()
		return result
	}
	defer os.RemoveAll(workspace)

	mutatedRules, err := file.Render(mutation)
	if err != nil {
		result.Status = domain.StatusError
		result.Output = err.Error()
		result.Duration = time.Since(started)
		return result
	}
	mutatedPath := filepath.Join(workspace, filepath.Base(file.Path))
	if err := os.WriteFile(mutatedPath, mutatedRules, 0o600); err != nil {
		result.Status = domain.StatusError
		result.Output = err.Error()
		result.Duration = time.Since(started)
		return result
	}

	if output, err := r.command(ctx, "check", "rules", mutatedPath); err != nil {
		result.Status = statusForError(err)
		result.Output = output
		result.Duration = time.Since(started)
		return result
	}

	tested := false
	for i, testFile := range testFiles {
		preparedPath := filepath.Join(workspace, fmt.Sprintf("test-%03d.yml", i))
		referencesTarget, err := prepareTestFile(testFile, file.Path, mutatedPath, preparedPath)
		if err != nil {
			result.Status = domain.StatusError
			result.Output = err.Error()
			result.TestFile = testFile
			result.Duration = time.Since(started)
			return result
		}
		if !referencesTarget {
			continue
		}
		tested = true

		output, err := r.command(ctx, "test", "rules", preparedPath)
		if err != nil {
			result.Status = statusForTestError(err)
			result.Output = output
			result.TestFile = testFile
			result.Duration = time.Since(started)
			return result
		}
	}
	if !tested {
		result.Status = domain.StatusError
		result.Output = fmt.Sprintf("no supplied test file references rule file %q", file.Path)
		result.Duration = time.Since(started)
		return result
	}

	result.Status = domain.StatusSurvived
	result.Duration = time.Since(started)
	if survivorsDir != "" {
		if err := persistSurvivor(survivorsDir, mutation, mutatedRules); err != nil {
			result.Status = domain.StatusError
			result.Output = fmt.Sprintf("persist survivor: %v", err)
		}
	}
	return result
}

func (r Runner) command(parent context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.Binary, args...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	if ctx.Err() != nil {
		return combined.String(), ctx.Err()
	}
	return combined.String(), err
}

func statusForError(err error) domain.Status {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.StatusTimeout
	}
	return domain.StatusError
}

func statusForTestError(err error) domain.Status {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.StatusTimeout
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return domain.StatusKilled
	}
	return domain.StatusError
}

func prepareTestFile(source, originalRule, mutatedRule, destination string) (bool, error) {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return false, fmt.Errorf("resolve test file %q: %w", source, err)
	}
	data, err := os.ReadFile(absSource)
	if err != nil {
		return false, fmt.Errorf("read test file %q: %w", absSource, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("parse test file %q: %w", absSource, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, fmt.Errorf("test file %q must contain a YAML mapping", absSource)
	}

	ruleFiles := mappingValue(doc.Content[0], "rule_files")
	if ruleFiles == nil || ruleFiles.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("test file %q has no rule_files sequence", absSource)
	}

	originalRule, err = filepath.Abs(originalRule)
	if err != nil {
		return false, err
	}
	base := filepath.Dir(absSource)
	var resolved []string
	referencesTarget := false
	for _, entry := range ruleFiles.Content {
		if entry.Kind != yaml.ScalarNode {
			continue
		}
		pattern := entry.Value
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(base, pattern)
		}
		matches, globErr := filepath.Glob(pattern)
		if globErr != nil {
			return false, fmt.Errorf("invalid rule_files glob %q: %w", entry.Value, globErr)
		}
		if len(matches) == 0 {
			matches = []string{filepath.Clean(pattern)}
		}
		for _, match := range matches {
			absMatch, absErr := filepath.Abs(match)
			if absErr != nil {
				return false, absErr
			}
			if samePath(absMatch, originalRule) {
				referencesTarget = true
				resolved = append(resolved, mutatedRule)
			} else {
				resolved = append(resolved, absMatch)
			}
		}
	}

	resolved = uniqueSorted(resolved)
	ruleFiles.Content = ruleFiles.Content[:0]
	for _, path := range resolved {
		ruleFiles.Content = append(ruleFiles.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: path,
		})
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return false, fmt.Errorf("encode prepared test: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return false, err
	}
	if err := os.WriteFile(destination, output.Bytes(), 0o600); err != nil {
		return false, fmt.Errorf("write prepared test: %w", err)
	}
	return referencesTarget, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func samePath(left, right string) bool {
	leftEval, leftErr := filepath.EvalSymlinks(left)
	rightEval, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return leftEval == rightEval
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func persistSurvivor(directory string, mutation domain.Mutation, content []byte) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-%s.yml", sanitize(mutation.Alert), sanitize(mutation.Operator), mutation.ID)
	return os.WriteFile(filepath.Join(directory, name), content, 0o600)
}

func sanitize(value string) string {
	value = strings.ToLower(value)
	var out strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			out.WriteRune(char)
		} else if out.Len() > 0 && !strings.HasSuffix(out.String(), "-") {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}
