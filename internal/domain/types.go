package domain

import "time"

// Objective lifecycle

type ObjectiveStatus string

const (
	ObjectiveStatusPlanning  ObjectiveStatus = "planning"
	ObjectiveStatusApproved  ObjectiveStatus = "approved"
	ObjectiveStatusExecuting ObjectiveStatus = "executing"
	ObjectiveStatusReviewing ObjectiveStatus = "reviewing"
	ObjectiveStatusCompleted ObjectiveStatus = "completed"
	ObjectiveStatusFailed    ObjectiveStatus = "failed"
)

type Objective struct {
	ID           string          `json:"id"`
	Description  string          `json:"description"`
	Status       ObjectiveStatus `json:"status"`
	Blueprint    string          `json:"blueprint"`
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
	ID           string    `json:"id"`
	PlanID       string    `json:"plan_id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	FileScope    []string  `json:"file_scope"`
	Dependencies []string  `json:"dependencies"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// Agent sessions

type AgentRole string

const (
	AgentRolePlanner AgentRole = "planner"
	AgentRoleLead    AgentRole = "lead"
	AgentRoleWorker  AgentRole = "worker"
	AgentRoleMerger  AgentRole = "merger"
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

type MailMessage struct {
	ID        int64     `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	Objective string    `json:"objective"`
	Stream    string    `json:"stream"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
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
