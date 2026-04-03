package domain

import "time"

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
	Description  string          `json:"description"`
	Status       ObjectiveStatus `json:"status"`
	Blueprint    string          `json:"blueprint,omitempty"`
	PlanningMode string          `json:"planning_mode,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
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
	ObjectiveID  string     `json:"objective_id"`
	Status       PlanStatus `json:"status"`
	QualityGates []string   `json:"quality_gates"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Stream struct {
	ID           string       `json:"id"`
	PlanID       string       `json:"plan_id"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	FileScope    []string     `json:"file_scope"`
	Dependencies []string     `json:"dependencies"`
	Status       StreamStatus `json:"status"`
	ExecutionID  string       `json:"execution_id,omitempty"` // sub-execution driving this stream
	CreatedAt    time.Time    `json:"created_at"`
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
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
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
	ObjectiveID string           `json:"objective_id"`
	Status      RunStatus        `json:"status"`
	Blocked     *BlockedState    `json:"blocked,omitempty"`
	Streams     []RunStreamState `json:"streams,omitempty"`
	Outcome     *Outcome         `json:"outcome,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
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
)

type Event struct {
	ID        int64     `json:"id"`
	Type      EventType `json:"type"`
	Objective string    `json:"objective"`
	Stream    string    `json:"stream"`
	Agent     string    `json:"agent"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}
