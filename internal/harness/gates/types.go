package gates

// Gate represents a single quality gate (a command to run in a sandbox).
type Gate struct {
	Name    string `yaml:"name" json:"name"`
	Command string `yaml:"command" json:"command"`
	Timeout int    `yaml:"timeout" json:"timeout"` // seconds, 0 = default (120s)
}

// GateResult holds the outcome of running a single gate.
type GateResult struct {
	Gate     Gate   `json:"gate"`
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int    `json:"duration_ms"`
}

// RunResult holds the aggregate outcome of running all gates.
type RunResult struct {
	AllPassed bool         `json:"all_passed"`
	Results   []GateResult `json:"results"`
}
