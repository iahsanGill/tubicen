package domain

import "time"

// Status describes the outcome of executing a single mutant against the test suite.
type Status string

const (
	StatusKilled   Status = "killed"
	StatusSurvived Status = "survived"
	StatusError    Status = "error"
	StatusTimeout  Status = "timeout"
)

// Mutation describes one deliberate defect introduced into an alerting rule.
type Mutation struct {
	ID          string `json:"id"`
	RuleFile    string `json:"rule_file"`
	Group       string `json:"group"`
	Alert       string `json:"alert"`
	Line        int    `json:"line,omitempty"`
	GroupIndex  int    `json:"-"`
	RuleIndex   int    `json:"-"`
	Operator    string `json:"operator"`
	Description string `json:"description"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	Expression  string `json:"expression,omitempty"`
	For         string `json:"for,omitempty"`
	RemoveFor   bool   `json:"-"`
}

// Result is the execution outcome for a mutation.
type Result struct {
	Mutation Mutation      `json:"mutation"`
	Status   Status        `json:"status"`
	Duration time.Duration `json:"duration_ns"`
	Output   string        `json:"output,omitempty"`
	TestFile string        `json:"test_file,omitempty"`
}

// Summary contains aggregate mutation statistics.
type Summary struct {
	Total      int     `json:"total"`
	Killed     int     `json:"killed"`
	Survived   int     `json:"survived"`
	Errors     int     `json:"errors"`
	Timeouts   int     `json:"timeouts"`
	Score      float64 `json:"score"`
	Threshold  float64 `json:"threshold"`
	PassedGate bool    `json:"passed_gate"`
}

// Report is the stable machine-readable result schema.
type Report struct {
	SchemaVersion string    `json:"schema_version"`
	ToolVersion   string    `json:"tool_version"`
	Promtool      string    `json:"promtool"`
	StartedAt     time.Time `json:"started_at"`
	Duration      string    `json:"duration"`
	RuleFiles     []string  `json:"rule_files"`
	TestFiles     []string  `json:"test_files"`
	Summary       Summary   `json:"summary"`
	Results       []Result  `json:"results"`
}
