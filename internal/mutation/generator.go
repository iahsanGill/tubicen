package mutation

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iahsanGill/tubicen/internal/domain"
	"github.com/iahsanGill/tubicen/internal/rules"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type candidate struct {
	operator    string
	description string
	original    string
	replacement string
	apply       func(parser.Expr) bool
}

type nodeKind uint8

const (
	binaryNode nodeKind = iota
	aggregateNode
	numberNode
	matrixNode
	callNode
)

// Generate creates deterministic, syntactically valid mutants for every alert in a rule file.
func Generate(file *rules.File) ([]domain.Mutation, error) {
	var mutations []domain.Mutation
	seen := make(map[string]struct{})

	for _, alert := range file.Alerts {
		exprCandidates, err := expressionCandidates(alert.Expression)
		if err != nil {
			return nil, fmt.Errorf("parse %s/%s: %w", alert.Group, alert.Name, err)
		}

		for _, c := range exprCandidates {
			tree, err := parseExpression(alert.Expression)
			if err != nil {
				return nil, err
			}
			if !c.apply(tree) {
				return nil, fmt.Errorf("internal error: mutation target %q disappeared", c.operator)
			}
			mutatedExpression := tree.String()
			if mutatedExpression == alert.Expression {
				continue
			}
			if _, err := parseExpression(mutatedExpression); err != nil {
				continue
			}

			m := newMutation(file.Path, alert, c.operator, c.description, c.original, c.replacement)
			m.Expression = mutatedExpression
			addUnique(&mutations, seen, m)
		}

		for _, m := range durationMutations(file.Path, alert) {
			addUnique(&mutations, seen, m)
		}
	}

	sort.Slice(mutations, func(i, j int) bool {
		if mutations[i].Group != mutations[j].Group {
			return mutations[i].Group < mutations[j].Group
		}
		if mutations[i].Alert != mutations[j].Alert {
			return mutations[i].Alert < mutations[j].Alert
		}
		if mutations[i].Operator != mutations[j].Operator {
			return mutations[i].Operator < mutations[j].Operator
		}
		return mutations[i].ID < mutations[j].ID
	})
	return mutations, nil
}

func expressionCandidates(expression string) ([]candidate, error) {
	tree, err := parseExpression(expression)
	if err != nil {
		return nil, err
	}

	var candidates []candidate
	var binaryIndex, aggregateIndex, numberIndex, matrixIndex, callIndex, matcherIndex int
	parser.Inspect(tree, func(node parser.Node, path []parser.Node) error {
		switch n := node.(type) {
		case *parser.BinaryExpr:
			idx := binaryIndex
			binaryIndex++
			for _, replacement := range binaryReplacements(n.Op) {
				to := replacement
				from := n.Op
				kind := "comparison.replace"
				if isLogical(from) {
					kind = "logical.replace"
				}
				candidates = append(candidates, candidate{
					operator:    kind,
					description: fmt.Sprintf("replace binary operator %s with %s", from, to),
					original:    from.String(),
					replacement: to.String(),
					apply: func(expr parser.Expr) bool {
						return mutateNth(expr, binaryNode, idx, func(node parser.Node) bool {
							binary, ok := node.(*parser.BinaryExpr)
							if ok {
								binary.Op = to
							}
							return ok
						})
					},
				})
			}

		case *parser.AggregateExpr:
			idx := aggregateIndex
			aggregateIndex++
			if replacement, ok := aggregateReplacement(n.Op); ok {
				from, to := n.Op, replacement
				candidates = append(candidates, candidate{
					operator:    "aggregation.replace",
					description: fmt.Sprintf("replace aggregation %s with %s", from, to),
					original:    from.String(),
					replacement: to.String(),
					apply: func(expr parser.Expr) bool {
						return mutateNth(expr, aggregateNode, idx, func(node parser.Node) bool {
							aggregate, ok := node.(*parser.AggregateExpr)
							if ok {
								aggregate.Op = to
							}
							return ok
						})
					},
				})
			}

		case *parser.NumberLiteral:
			idx := numberIndex
			numberIndex++
			if !isComparisonThreshold(n, path) {
				break
			}
			for _, replacement := range thresholdReplacements(n.Val) {
				from, to := n.Val, replacement
				kind := thresholdOperator(from, to)
				candidates = append(candidates, candidate{
					operator:    kind,
					description: fmt.Sprintf("change comparison threshold from %s to %s", formatNumber(from), formatNumber(to)),
					original:    formatNumber(from),
					replacement: formatNumber(to),
					apply: func(expr parser.Expr) bool {
						return mutateNth(expr, numberNode, idx, func(node parser.Node) bool {
							number, ok := node.(*parser.NumberLiteral)
							if ok {
								number.Val = to
							}
							return ok
						})
					},
				})
			}

		case *parser.MatrixSelector:
			idx := matrixIndex
			matrixIndex++
			if n.RangeExpr != nil || n.Range <= 0 {
				break
			}
			for _, replacement := range rangeReplacements(n.Range) {
				from, to := n.Range, replacement
				kind := "range.expand"
				if to < from {
					kind = "range.contract"
				}
				candidates = append(candidates, candidate{
					operator:    kind,
					description: fmt.Sprintf("change range window from %s to %s", model.Duration(from), model.Duration(to)),
					original:    model.Duration(from).String(),
					replacement: model.Duration(to).String(),
					apply: func(expr parser.Expr) bool {
						return mutateNth(expr, matrixNode, idx, func(node parser.Node) bool {
							matrix, ok := node.(*parser.MatrixSelector)
							if ok {
								matrix.Range = to
							}
							return ok
						})
					},
				})
			}

		case *parser.Call:
			idx := callIndex
			callIndex++
			if replacement, ok := functionReplacement(n.Func.Name); ok {
				from, to := n.Func.Name, replacement
				candidates = append(candidates, candidate{
					operator:    "function.replace",
					description: fmt.Sprintf("replace function %s with %s", from, to),
					original:    from,
					replacement: to,
					apply: func(expr parser.Expr) bool {
						return mutateNth(expr, callNode, idx, func(node parser.Node) bool {
							call, ok := node.(*parser.Call)
							if ok {
								call.Func = parser.Functions[to]
							}
							return ok
						})
					},
				})
			}

		case *parser.VectorSelector:
			for _, matcher := range n.LabelMatchers {
				idx := matcherIndex
				matcherIndex++
				if matcher.Name == "__name__" {
					continue
				}
				replacement := negateMatcher(matcher.Type)
				from, to := matcher.Type, replacement
				name, value := matcher.Name, matcher.Value
				candidates = append(candidates, candidate{
					operator:    "selector.negate",
					description: fmt.Sprintf("negate label matcher %s%s%q", name, from, value),
					original:    fmt.Sprintf("%s%s%q", name, from, value),
					replacement: fmt.Sprintf("%s%s%q", name, to, value),
					apply: func(expr parser.Expr) bool {
						return mutateNthMatcher(expr, idx, to)
					},
				})
			}
		}
		return nil
	})
	return candidates, nil
}

func mutateNth(expr parser.Expr, kind nodeKind, target int, mutate func(parser.Node) bool) bool {
	current := 0
	changed := false
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if node == nil || changed {
			return nil
		}
		match := false
		switch kind {
		case binaryNode:
			_, match = node.(*parser.BinaryExpr)
		case aggregateNode:
			_, match = node.(*parser.AggregateExpr)
		case numberNode:
			_, match = node.(*parser.NumberLiteral)
		case matrixNode:
			_, match = node.(*parser.MatrixSelector)
		case callNode:
			_, match = node.(*parser.Call)
		}
		if !match {
			return nil
		}
		if current == target && mutate(node) {
			changed = true
			return nil
		}
		current++
		return nil
	})
	return changed
}

func mutateNthMatcher(expr parser.Expr, target int, replacement labels.MatchType) bool {
	current := 0
	changed := false
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		selector, ok := node.(*parser.VectorSelector)
		if !ok || changed {
			return nil
		}
		for _, matcher := range selector.LabelMatchers {
			if current == target {
				matcher.Type = replacement
				changed = true
				return nil
			}
			current++
		}
		return nil
	})
	return changed
}

func binaryReplacements(op parser.ItemType) []parser.ItemType {
	switch op {
	case parser.GTR:
		return []parser.ItemType{parser.GTE, parser.LSS}
	case parser.GTE:
		return []parser.ItemType{parser.GTR, parser.LTE}
	case parser.LSS:
		return []parser.ItemType{parser.LTE, parser.GTR}
	case parser.LTE:
		return []parser.ItemType{parser.LSS, parser.GTE}
	case parser.EQLC:
		return []parser.ItemType{parser.NEQ}
	case parser.NEQ:
		return []parser.ItemType{parser.EQLC}
	case parser.LAND:
		return []parser.ItemType{parser.LOR, parser.LUNLESS}
	case parser.LOR:
		return []parser.ItemType{parser.LAND}
	case parser.LUNLESS:
		return []parser.ItemType{parser.LAND}
	default:
		return nil
	}
}

func isLogical(op parser.ItemType) bool {
	return op == parser.LAND || op == parser.LOR || op == parser.LUNLESS
}

func isComparison(op parser.ItemType) bool {
	switch op {
	case parser.GTR, parser.GTE, parser.LSS, parser.LTE, parser.EQLC, parser.NEQ:
		return true
	default:
		return false
	}
}

func isComparisonThreshold(number *parser.NumberLiteral, path []parser.Node) bool {
	if len(path) == 0 {
		return false
	}
	parent, ok := path[len(path)-1].(*parser.BinaryExpr)
	if !ok || !isComparison(parent.Op) {
		return false
	}
	return parent.LHS == number || parent.RHS == number
}

func aggregateReplacement(op parser.ItemType) (parser.ItemType, bool) {
	switch op {
	case parser.SUM:
		return parser.AVG, true
	case parser.AVG:
		return parser.SUM, true
	case parser.MAX:
		return parser.MIN, true
	case parser.MIN:
		return parser.MAX, true
	default:
		return 0, false
	}
}

func functionReplacement(name string) (string, bool) {
	replacements := map[string]string{
		"rate":          "irate",
		"irate":         "rate",
		"sum_over_time": "avg_over_time",
		"avg_over_time": "sum_over_time",
		"max_over_time": "min_over_time",
		"min_over_time": "max_over_time",
	}
	replacement, ok := replacements[name]
	return replacement, ok
}

func negateMatcher(match labels.MatchType) labels.MatchType {
	switch match {
	case labels.MatchEqual:
		return labels.MatchNotEqual
	case labels.MatchNotEqual:
		return labels.MatchEqual
	case labels.MatchRegexp:
		return labels.MatchNotRegexp
	default:
		return labels.MatchRegexp
	}
}

func thresholdReplacements(value float64) []float64 {
	if value == 0 {
		return []float64{-1, 1}
	}
	return []float64{value / 2, value * 10}
}

func thresholdOperator(from, to float64) string {
	if from == 0 {
		if to < from {
			return "threshold.shift-down"
		}
		return "threshold.shift-up"
	}
	if math.Abs(to) < math.Abs(from) {
		return "threshold.scale-down"
	}
	return "threshold.scale-up"
}

func rangeReplacements(value time.Duration) []time.Duration {
	contracted := value / 2
	if contracted < time.Second {
		contracted = time.Second
	}
	return []time.Duration{contracted, value * 2}
}

func durationMutations(path string, alert rules.Alert) []domain.Mutation {
	if alert.For == "" {
		m := newMutation(path, alert, "for.add", "add a five-minute firing delay", "none", "5m")
		m.For = "5m"
		return []domain.Mutation{m}
	}

	duration, err := model.ParseDuration(alert.For)
	if err != nil || duration <= 0 {
		return nil
	}
	original := duration.String()
	removed := newMutation(path, alert, "for.remove", "remove the firing delay", original, "none")
	removed.RemoveFor = true

	contractedDuration := time.Duration(duration) / 2
	if contractedDuration < time.Second {
		contractedDuration = time.Second
	}
	contracted := model.Duration(contractedDuration).String()
	expanded := model.Duration(time.Duration(duration) * 2).String()

	contract := newMutation(path, alert, "for.contract", "halve the firing delay", original, contracted)
	contract.For = contracted
	expand := newMutation(path, alert, "for.expand", "double the firing delay", original, expanded)
	expand.For = expanded
	return []domain.Mutation{removed, contract, expand}
}

func newMutation(path string, alert rules.Alert, operator, description, original, replacement string) domain.Mutation {
	identity := strings.Join([]string{
		stablePath(path), alert.Group, alert.Name, operator, original, replacement,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return domain.Mutation{
		ID:          fmt.Sprintf("%x", sum[:6]),
		RuleFile:    path,
		Group:       alert.Group,
		Alert:       alert.Name,
		Line:        alert.Line,
		GroupIndex:  alert.GroupIndex,
		RuleIndex:   alert.RuleIndex,
		Operator:    operator,
		Description: description,
		Original:    original,
		Replacement: replacement,
	}
}

func stablePath(path string) string {
	cleaned := filepath.Clean(path)
	workingDirectory, err := os.Getwd()
	if err == nil {
		if relative, relErr := filepath.Rel(workingDirectory, cleaned); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			cleaned = relative
		}
	}
	return filepath.ToSlash(cleaned)
}

func addUnique(mutations *[]domain.Mutation, seen map[string]struct{}, mutation domain.Mutation) {
	key := strings.Join([]string{mutation.RuleFile, mutation.Group, mutation.Alert, mutation.Expression, mutation.For, fmt.Sprint(mutation.RemoveFor)}, "\x00")
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*mutations = append(*mutations, mutation)
}

func formatNumber(value float64) string {
	return fmt.Sprintf("%g", value)
}

func parseExpression(input string) (parser.Expr, error) {
	return parser.NewParser(parser.Options{}).ParseExpr(input)
}
