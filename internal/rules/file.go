package rules

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iahsanGill/tubicen/internal/domain"
	"gopkg.in/yaml.v3"
)

// Alert identifies an alerting rule inside a Prometheus rule file.
type Alert struct {
	Group      string
	Name       string
	Expression string
	For        string
	Line       int
	GroupIndex int
	RuleIndex  int
}

// File is a parsed Prometheus rule file that preserves its YAML representation.
type File struct {
	Path     string
	document *yaml.Node
	Alerts   []Alert
}

// Load parses a Prometheus rule file and extracts all alerting rules.
func Load(path string) (*File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve rule file: %w", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read rule file %q: %w", abs, err)
	}

	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse rule file %q: %w", abs, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("rule file %q must contain a YAML mapping", abs)
	}

	f := &File{Path: abs, document: &doc}
	groups := mappingValue(doc.Content[0], "groups")
	if groups == nil || groups.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("rule file %q has no groups sequence", abs)
	}

	for gi, groupNode := range groups.Content {
		if groupNode.Kind != yaml.MappingNode {
			continue
		}
		groupName := scalarValue(mappingValue(groupNode, "name"))
		ruleSeq := mappingValue(groupNode, "rules")
		if ruleSeq == nil || ruleSeq.Kind != yaml.SequenceNode {
			continue
		}
		for ri, ruleNode := range ruleSeq.Content {
			if ruleNode.Kind != yaml.MappingNode {
				continue
			}
			alertName := scalarValue(mappingValue(ruleNode, "alert"))
			if alertName == "" {
				continue
			}
			exprNode := mappingValue(ruleNode, "expr")
			expr := scalarValue(exprNode)
			if expr == "" {
				return nil, fmt.Errorf("alert %q in group %q has no expression", alertName, groupName)
			}
			f.Alerts = append(f.Alerts, Alert{
				Group:      groupName,
				Name:       alertName,
				Expression: expr,
				For:        scalarValue(mappingValue(ruleNode, "for")),
				Line:       exprNode.Line,
				GroupIndex: gi,
				RuleIndex:  ri,
			})
		}
	}

	if len(f.Alerts) == 0 {
		return nil, fmt.Errorf("rule file %q contains no alerting rules", abs)
	}
	return f, nil
}

// Render returns a copy of the rule file with one mutation applied.
func (f *File) Render(m domain.Mutation) ([]byte, error) {
	doc := cloneNode(f.document)
	ruleNode, err := locateRule(doc, m.GroupIndex, m.RuleIndex)
	if err != nil {
		return nil, err
	}

	if m.Expression != "" {
		expr := mappingValue(ruleNode, "expr")
		if expr == nil {
			return nil, errors.New("target rule has no expr field")
		}
		expr.Value = m.Expression
	}

	if m.RemoveFor {
		removeMappingKey(ruleNode, "for")
	} else if m.For != "" {
		setMappingScalar(ruleNode, "for", m.For)
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode mutated rule file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close YAML encoder: %w", err)
	}
	return out.Bytes(), nil
}

func locateRule(doc *yaml.Node, groupIndex, ruleIndex int) (*yaml.Node, error) {
	if len(doc.Content) == 0 {
		return nil, errors.New("empty rule document")
	}
	groups := mappingValue(doc.Content[0], "groups")
	if groups == nil || groupIndex < 0 || groupIndex >= len(groups.Content) {
		return nil, fmt.Errorf("group index %d is out of range", groupIndex)
	}
	rules := mappingValue(groups.Content[groupIndex], "rules")
	if rules == nil || ruleIndex < 0 || ruleIndex >= len(rules.Content) {
		return nil, fmt.Errorf("rule index %d is out of range", ruleIndex)
	}
	return rules.Content[ruleIndex], nil
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

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func setMappingScalar(mapping *yaml.Node, key, value string) {
	if existing := mappingValue(mapping, key); existing != nil {
		existing.Value = value
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func removeMappingKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func cloneNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneNode(child)
	}
	return &clone
}
