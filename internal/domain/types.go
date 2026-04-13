package domain

import "time"

const streamAcceptanceCriteriaMarker = "\n\n[TACK_ACCEPTANCE_CRITERIA]\n"

type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	RootPath   string    `json:"root_path"`
	ConfigPath string    `json:"config_path"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Objective lifecycle

type ObjectiveStatus string

const (
	ObjectiveStatusPlanning  ObjectiveStatus = "planning"
	ObjectiveStatusApproved  ObjectiveStatus = "approved"
	ObjectiveStatusExecuting ObjectiveStatus = "executing"
	ObjectiveStatusCompleted ObjectiveStatus = "completed"
	ObjectiveStatusPartial   ObjectiveStatus = "partial"
	ObjectiveStatusFailed    ObjectiveStatus = "failed"
)

type Objective struct {
	ID           string          `json:"id"`
	ProjectID    string          `json:"project_id"`
	Description  string          `json:"description"`
	Status       ObjectiveStatus `json:"status"`
	Blueprint    string          `json:"blueprint,omitempty"`
	PlanningMode string          `json:"planning_mode,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type Dossier struct {
	ObjectiveID     string             `json:"objective_id"`
	ProjectID       string             `json:"project_id"`
	Summary         string             `json:"summary"`
	BlueprintID     string             `json:"blueprint_id,omitempty"`
	RepoPriors      []DossierPrior     `json:"repo_priors,omitempty"`
	RelevantFiles   []DossierReference `json:"relevant_files,omitempty"`
	SimilarPatterns []DossierReference `json:"similar_patterns,omitempty"`
	Risks           []string           `json:"risks,omitempty"`
	Unknowns        []string           `json:"unknowns,omitempty"`
	SuggestedSeams  []DossierSeam      `json:"suggested_seams,omitempty"`
	Citations       []DossierCitation  `json:"citations,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

type DossierPrior struct {
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	CitationIDs []string `json:"citation_ids,omitempty"`
}

type DossierReference struct {
	Path        string   `json:"path"`
	Reason      string   `json:"reason"`
	CitationIDs []string `json:"citation_ids,omitempty"`
}

type DossierSeam struct {
	Title       string   `json:"title"`
	Reason      string   `json:"reason"`
	FilePaths   []string `json:"file_paths,omitempty"`
	CitationIDs []string `json:"citation_ids,omitempty"`
}

type DossierCitation struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

type DossierExpansionRequest struct {
	Reason     string   `json:"reason"`
	FocusAreas []string `json:"focus_areas,omitempty"`
	FileHints  []string `json:"file_hints,omitempty"`
	Questions  []string `json:"questions,omitempty"`
}

// Plan and streams

type PlanStatus string

const (
	PlanStatusDraft           PlanStatus = "draft"
	PlanStatusPendingApproval PlanStatus = "pending_approval"
	PlanStatusApproved        PlanStatus = "approved"
	PlanStatusExecuting       PlanStatus = "executing"
	PlanStatusCompleted       PlanStatus = "completed"
	PlanStatusFailed          PlanStatus = "failed"
)

type Plan struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	ObjectiveID  string     `json:"objective_id"`
	Status       PlanStatus `json:"status"`
	QualityGates []string   `json:"quality_gates"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Stream struct {
	ID                 string       `json:"id"`
	ProjectID          string       `json:"project_id"`
	PlanID             string       `json:"plan_id"`
	Title              string       `json:"title"`
	Description        string       `json:"description"`
	Card               *StreamCard  `json:"card,omitempty"`
	AcceptanceCriteria []string     `json:"acceptance_criteria,omitempty"`
	FileScope          []string     `json:"file_scope"`
	Dependencies       []string     `json:"dependencies"`
	Status             StreamStatus `json:"status"`
	ExecutionID        string       `json:"execution_id,omitempty"` // sub-execution driving this stream
	CreatedAt          time.Time    `json:"created_at"`
}

type StreamCard struct {
	Goal                  string             `json:"goal"`
	BlockedBy             []string           `json:"blocked_by,omitempty"`
	AcceptanceCriteria    []string           `json:"acceptance_criteria,omitempty"`
	ImplementationScope   []string           `json:"implementation_scope,omitempty"`
	ProofScope            []string           `json:"proof_scope,omitempty"`
	HardAnchors           []StreamCardAnchor `json:"hard_anchors,omitempty"`
	SeamOverrideRationale string             `json:"seam_override_rationale,omitempty"`
}

type StreamCardAnchor struct {
	Instruction string               `json:"instruction"`
	Citations   []StreamCardCitation `json:"citations,omitempty"`
}

type StreamCardCitation struct {
	ID     string `json:"id"`
	Kind   string `json:"kind,omitempty"`
	Target string `json:"target,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (s Stream) EffectiveCard() StreamCard {
	if s.Card != nil {
		return *s.Card
	}
	card := StreamCard{
		Goal:                s.Description,
		AcceptanceCriteria:  append([]string(nil), s.AcceptanceCriteria...),
		ImplementationScope: append([]string(nil), s.FileScope...),
		ProofScope:          append([]string(nil), s.AcceptanceCriteria...),
	}
	return card
}

// StreamDescriptionPayload encodes acceptance criteria into the persisted
// description field while keeping the user-facing description clean.
func StreamDescriptionPayload(description string, acceptanceCriteria []string) string {
	description = trimTrailingNewlines(description)
	if len(acceptanceCriteria) == 0 {
		return description
	}
	payload := description + streamAcceptanceCriteriaMarker
	for _, item := range acceptanceCriteria {
		if item == "" {
			continue
		}
		payload += "- " + item + "\n"
	}
	return trimTrailingNewlines(payload)
}

// ParseStreamDescriptionPayload extracts acceptance criteria from the encoded
// description payload.
func ParseStreamDescriptionPayload(payload string) (string, []string) {
	idx := indexOfAcceptanceMarker(payload)
	if idx == -1 {
		return trimTrailingNewlines(payload), nil
	}
	description := trimTrailingNewlines(payload[:idx])
	block := payload[idx+len(streamAcceptanceCriteriaMarker):]
	var criteria []string
	current := ""
	for _, line := range splitLines(block) {
		trimmed := trimSpace(line)
		if trimmed == "" {
			continue
		}
		if hasPrefix(trimmed, "- ") {
			if current != "" {
				criteria = append(criteria, current)
			}
			current = trimmed[2:]
			continue
		}
		if current != "" {
			current += " " + trimmed
		}
	}
	if current != "" {
		criteria = append(criteria, current)
	}
	return description, criteria
}

func indexOfAcceptanceMarker(payload string) int {
	for i := 0; i+len(streamAcceptanceCriteriaMarker) <= len(payload); i++ {
		if payload[i:i+len(streamAcceptanceCriteriaMarker)] == streamAcceptanceCriteriaMarker {
			return i
		}
	}
	return -1
}

func splitLines(s string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimTrailingNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func hasPrefix(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// Agent sessions

type AgentRole string

const (
	AgentRolePlanner  AgentRole = "planner"
	AgentRoleLead     AgentRole = "lead"
	AgentRoleBuilder  AgentRole = "builder"
	AgentRoleReviewer AgentRole = "reviewer"
	AgentRoleMerger   AgentRole = "merger"
	AgentRoleScout    AgentRole = "scout"
)

type AgentSession struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ObjectiveID string    `json:"objective_id"`
	StreamID    string    `json:"stream_id"`
	Role        AgentRole `json:"role"`
	SandboxID   string    `json:"sandbox_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Mail messages

type MailType string

const (
	MailTypeMessage    MailType = "message"
	MailTypeStatus     MailType = "status"
	MailTypeDispatch   MailType = "dispatch"
	MailTypeWorkerDone MailType = "worker_done"
	MailTypeMergeReady MailType = "merge_ready"
	MailTypeEscalation MailType = "escalation"
	MailTypeQuestion   MailType = "question"
)

type MailPriority string

const (
	MailPriorityLow    MailPriority = "low"
	MailPriorityNormal MailPriority = "normal"
	MailPriorityHigh   MailPriority = "high"
	MailPriorityUrgent MailPriority = "urgent"
)

type MailMessage struct {
	ID        int64     `json:"id"`
	ProjectID string    `json:"project_id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	Type      string    `json:"type"`
	Priority  string    `json:"priority"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Payload   string    `json:"payload,omitempty"`
	DedupKey  string    `json:"dedup_key,omitempty"`
	Objective string    `json:"objective"`
	Stream    string    `json:"stream"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// Stream statuses

type StreamStatus string

const (
	StreamStatusPending    StreamStatus = "pending"
	StreamStatusExecuting  StreamStatus = "executing"
	StreamStatusCompleted  StreamStatus = "completed"
	StreamStatusFailed     StreamStatus = "failed"
	StreamStatusMergeReady StreamStatus = "merge_ready"
	StreamStatusMerging    StreamStatus = "merging"
	StreamStatusMerged     StreamStatus = "merged"
)

// streamValidTransitions defines all valid state transitions for streams.
var streamValidTransitions = map[StreamStatus][]StreamStatus{
	StreamStatusPending:    {StreamStatusExecuting, StreamStatusFailed},
	StreamStatusExecuting:  {StreamStatusCompleted, StreamStatusFailed, StreamStatusMergeReady},
	StreamStatusCompleted:  {StreamStatusMergeReady, StreamStatusFailed},
	StreamStatusMergeReady: {StreamStatusMerging, StreamStatusFailed},
	StreamStatusMerging:    {StreamStatusMerged, StreamStatusFailed, StreamStatusMergeReady},
	StreamStatusFailed:     {StreamStatusPending},
	// StreamStatusMerged is terminal — no outbound transitions.
}

// IsValidStreamTransition returns true if transitioning from → to is allowed.
func IsValidStreamTransition(from, to StreamStatus) bool {
	for _, valid := range streamValidTransitions[from] {
		if valid == to {
			return true
		}
	}
	return false
}

// Merge queue

type MergeStatus string

const (
	MergeStatusPending  MergeStatus = "pending"
	MergeStatusMerging  MergeStatus = "merging"
	MergeStatusMerged   MergeStatus = "merged"
	MergeStatusFailed   MergeStatus = "failed"
	MergeStatusConflict MergeStatus = "conflict"
)

// MergeEntry represents a stream branch queued for merge.
type MergeEntry struct {
	ID          string      `json:"id"`
	ProjectID   string      `json:"project_id"`
	StreamID    string      `json:"stream_id"`
	PlanID      string      `json:"plan_id"`
	ObjectiveID string      `json:"objective_id"`
	Branch      string      `json:"branch"`
	Status      MergeStatus `json:"status"`
	Tier        int         `json:"tier"`
	Error       string      `json:"error"`
	DiffStat    string      `json:"diff_stat"`
	CreatedAt   int64       `json:"created_at"`
	UpdatedAt   int64       `json:"updated_at"`
}

// Run-centric orchestration

type RunStatus string

const (
	RunStatusActive    RunStatus = "active"
	RunStatusBlocked   RunStatus = "blocked"
	RunStatusCompleted RunStatus = "completed"
	RunStatusPartial   RunStatus = "partial"
	RunStatusFailed    RunStatus = "failed"
)

// Run is the durable aggregate that owns objective orchestration.
type Run struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ObjectiveID string    `json:"objective_id"`
	Status      RunStatus `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CommandKind identifies the type of intervention on a run.
type CommandKind string

const (
	CommandApprove CommandKind = "approve"
	CommandRetry   CommandKind = "retry"
	CommandAbort   CommandKind = "abort"
	CommandKill    CommandKind = "kill"
)

// Command represents an intervention on a run.
type Command struct {
	Kind      CommandKind `json:"kind"`
	StreamID  string      `json:"stream_id,omitempty"`
	SessionID string      `json:"session_id,omitempty"` // for kill: target agent session
	Guidance  string      `json:"guidance,omitempty"`
	Reason    string      `json:"reason,omitempty"`
}

// BlockedState describes why a run is blocked.
type BlockedState struct {
	Kind      string `json:"kind"`
	Reason    string `json:"reason,omitempty"`
	StreamID  string `json:"stream_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
}

// Outcome describes the terminal result of a run.
type Outcome struct {
	Status  RunStatus `json:"status"`
	Summary string    `json:"summary,omitempty"`
}

// RunStreamState is a snapshot of a single stream within a run.
type RunStreamState struct {
	StreamID  string       `json:"stream_id"`
	Title     string       `json:"title"`
	Status    StreamStatus `json:"status"`
	Error     string       `json:"error,omitempty"`
	Retryable bool         `json:"retryable,omitempty"`
}

// Snapshot is the observable state of a run at a point in time.
type Snapshot struct {
	RunID       string           `json:"run_id"`
	ProjectID   string           `json:"project_id"`
	ObjectiveID string           `json:"objective_id"`
	Status      RunStatus        `json:"status"`
	Blocked     *BlockedState    `json:"blocked,omitempty"`
	Streams     []RunStreamState `json:"streams,omitempty"`
	Outcome     *Outcome         `json:"outcome,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// Recovery and retry

type FailureKind string

const (
	FailureAgentRuntimeTransient FailureKind = "agent_runtime_transient"
	FailureAgentOutput           FailureKind = "agent_output_failure"
	FailureQualityGate           FailureKind = "quality_gate_failure"
	FailureReviewRejection       FailureKind = "review_rejection"
	FailureSandbox               FailureKind = "sandbox_failure"
	FailureProviderRateLimit     FailureKind = "provider_rate_limit"
	FailureMergeConflict         FailureKind = "merge_conflict"
	FailurePostMergeGate         FailureKind = "post_merge_gate_failure"
	FailureDeterministicStep     FailureKind = "deterministic_step_failure"
)

type RecoveryAction string

const (
	RecoveryActionRetrySameStep      RecoveryAction = "retry_same_step"
	RecoveryActionRerunPreviousAgent RecoveryAction = "rerun_previous_agent"
	RecoveryActionRestartStream      RecoveryAction = "restart_stream"
	RecoveryActionRetryMerge         RecoveryAction = "retry_merge"
	RecoveryActionAskHumanThenResume RecoveryAction = "ask_human_then_resume"
	RecoveryActionFailTerminal       RecoveryAction = "fail_terminal"
)

type ExhaustionMode string

const (
	ExhaustionAskHuman ExhaustionMode = "ask_human"
	ExhaustionEscalate ExhaustionMode = "escalate"
	ExhaustionFail     ExhaustionMode = "fail"
)

type AttemptStatus string

const (
	AttemptStatusRecorded  AttemptStatus = "recorded"
	AttemptStatusRunning   AttemptStatus = "running"
	AttemptStatusBlocked   AttemptStatus = "blocked"
	AttemptStatusSucceeded AttemptStatus = "succeeded"
	AttemptStatusFailed    AttemptStatus = "failed"
	AttemptStatusExhausted AttemptStatus = "exhausted"
)

// Attempt is an append-only recovery ledger record.
type Attempt struct {
	ID                 string         `json:"id"`
	ProjectID          string         `json:"project_id"`
	ObjectiveID        string         `json:"objective_id"`
	RunID              string         `json:"run_id,omitempty"`
	ExecutionID        string         `json:"execution_id,omitempty"`
	StreamID           string         `json:"stream_id,omitempty"`
	StepID             string         `json:"step_id,omitempty"`
	MergeEntryID       string         `json:"merge_entry_id,omitempty"`
	AttemptNumber      int            `json:"attempt_number"`
	MaxAttempts        int            `json:"max_attempts"`
	FailureKind        FailureKind    `json:"failure_kind"`
	Action             RecoveryAction `json:"action"`
	Status             AttemptStatus  `json:"status"`
	ErrorSummary       string         `json:"error_summary,omitempty"`
	FixContext         string         `json:"fix_context,omitempty"`
	HumanGuidance      string         `json:"human_guidance,omitempty"`
	TriggeredByAttempt string         `json:"triggered_by_attempt_id,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
}

// Events

type EventType string

const (
	EventObjectiveCreated EventType = "objective.created"
	EventObjectiveUpdated EventType = "objective.updated"
	EventPlanCreated      EventType = "plan.created"
	EventPlanApproved     EventType = "plan.approved"
	EventAgentSpawned     EventType = "agent.spawned"
	EventAgentCompleted   EventType = "agent.completed"
	EventAgentFailed      EventType = "agent.failed"
	EventMailSent         EventType = "mail.sent"
	EventMergeQueued      EventType = "merge.queued"
	EventMergeCompleted   EventType = "merge.completed"
	EventMergeFailed      EventType = "merge.failed"
	EventEscalation       EventType = "escalation"
	EventStreamReady      EventType = "stream.ready"
	EventExecutionStarted EventType = "execution.started"
	EventAgentActivity    EventType = "agent.activity"
	EventRecoveryAttempt  EventType = "recovery.attempt"
	EventRecoveryBlocked  EventType = "recovery.blocked"
	EventRecoveryResumed  EventType = "recovery.resumed"
)

type Event struct {
	ID        int64     `json:"id"`
	ProjectID string    `json:"project_id"`
	Type      EventType `json:"type"`
	Objective string    `json:"objective"`
	Stream    string    `json:"stream"`
	Agent     string    `json:"agent"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}
