// Package domain contains the public, transport-neutral M1 facts.
package domain

type Object map[string]any

type LeasePolicy struct {
	MinSeconds int64 `json:"min_seconds"`
	MaxSeconds int64 `json:"max_seconds"`
}

type Contract struct {
	Profile               string      `json:"profile"`
	OwnerPrincipalID      string      `json:"owner_principal_id"`
	Goal                  string      `json:"goal"`
	Constraints           []string    `json:"constraints"`
	InputRefs             []Object    `json:"input_refs"`
	ExpectedOutput        string      `json:"expected_output"`
	AcceptanceRefs        []Object    `json:"acceptance_refs"`
	ContextRefs           []Object    `json:"context_refs"`
	EligibleAgentIDs      []string    `json:"eligible_agent_ids"`
	AcceptancePrincipalID string      `json:"acceptance_principal_id"`
	RecoveryPolicy        string      `json:"recovery_policy"`
	AllowRework           bool        `json:"allow_rework"`
	LeasePolicy           LeasePolicy `json:"lease_policy"`
	Extensions            Object      `json:"extensions"`
}

type Task struct {
	TaskID              string   `json:"task_id"`
	State               string   `json:"state"`
	Revision            int64    `json:"revision"`
	Generation          int64    `json:"generation"`
	ContentVersion      *int64   `json:"content_version"`
	ContractDigest      *string  `json:"contract_digest"`
	Contract            Contract `json:"contract"`
	CurrentAssignmentID *string  `json:"current_assignment_id"`
	PendingSubmissionID *string  `json:"pending_submission_id"`
	RecoveryGate        string   `json:"recovery_gate"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

type Assignment struct {
	AssignmentID   string  `json:"assignment_id"`
	TaskID         string  `json:"task_id"`
	PrincipalID    string  `json:"principal_id"`
	AgentID        string  `json:"agent_id"`
	ClaimGrantID   string  `json:"claim_grant_id"`
	State          string  `json:"state"`
	Generation     int64   `json:"generation"`
	LeaseExpiresAt string  `json:"lease_expires_at"`
	CreatedAt      string  `json:"created_at"`
	EndedAt        *string `json:"ended_at"`
	EndReason      *string `json:"end_reason"`
	EffectiveAt    *string `json:"effective_at"`
	BindingID      string  `json:"binding_id"`
}

type Run struct {
	RunID        string  `json:"run_id"`
	TaskID       string  `json:"task_id"`
	AssignmentID string  `json:"assignment_id"`
	State        string  `json:"state"`
	StartedAt    string  `json:"started_at"`
	EndedAt      *string `json:"ended_at"`
	EndReason    *string `json:"end_reason"`
}

type Report struct {
	ReportID     string   `json:"report_id"`
	TaskID       string   `json:"task_id"`
	RunID        string   `json:"run_id"`
	AssignmentID string   `json:"assignment_id"`
	Message      string   `json:"message"`
	EvidenceRefs []Object `json:"evidence_refs"`
	Extensions   Object   `json:"extensions"`
	ReportedAt   string   `json:"reported_at"`
}

type Submission struct {
	SubmissionID   string   `json:"submission_id"`
	TaskID         string   `json:"task_id"`
	RunID          string   `json:"run_id"`
	AssignmentID   string   `json:"assignment_id"`
	ContentVersion int64    `json:"content_version"`
	Summary        string   `json:"summary"`
	ArtifactRefs   []Object `json:"artifact_refs"`
	EvidenceRefs   []Object `json:"evidence_refs"`
	Extensions     Object   `json:"extensions"`
	SubmittedAt    string   `json:"submitted_at"`
}

type Decision struct {
	DecisionID   string   `json:"decision_id"`
	TaskID       string   `json:"task_id"`
	SubmissionID string   `json:"submission_id"`
	Outcome      string   `json:"outcome"`
	PrincipalID  string   `json:"principal_id"`
	Reason       string   `json:"reason"`
	EvidenceRefs []Object `json:"evidence_refs"`
	DecidedAt    string   `json:"decided_at"`
}

type Relation struct {
	RelationID   string `json:"relation_id"`
	TaskID       string `json:"task_id"`
	Kind         string `json:"kind"`
	ParentTaskID string `json:"parent_task_id"`
	CreatedAt    string `json:"created_at"`
}

type Grant struct {
	GrantID             string   `json:"grant_id"`
	TaskID              string   `json:"task_id"`
	OwnerPrincipalID    string   `json:"owner_principal_id"`
	SubjectPrincipalID  string   `json:"subject_principal_id"`
	AgentID             *string  `json:"agent_id"`
	Actions             []string `json:"actions"`
	NotBefore           string   `json:"not_before"`
	ExpiresAt           string   `json:"expires_at"`
	IssuedByPrincipalID string   `json:"issued_by_principal_id"`
	RevokedAt           *string  `json:"revoked_at"`
}

type Actor struct {
	Kind             string   `json:"kind"`
	PrincipalID      *string  `json:"principal_id"`
	AgentID          *string  `json:"agent_id"`
	AuthorizationIDs []string `json:"authorization_ids"`
}

type Audit struct {
	Action    string  `json:"action"`
	RequestID *string `json:"request_id"`
	Reason    *string `json:"reason"`
}

type Record struct {
	RecordType string `json:"record_type"`
	Record     Object `json:"record"`
}
type Event struct {
	EventID      string `json:"event_id"`
	Type         string `json:"type"`
	TaskID       string `json:"task_id"`
	TaskRevision int64  `json:"task_revision"`
	RecordType   string `json:"record_type"`
	Record       Object `json:"record"`
}
type CommitBatch struct {
	CommitID    string  `json:"commit_id"`
	CommittedAt string  `json:"committed_at"`
	Actor       Actor   `json:"actor"`
	Audit       Audit   `json:"audit"`
	Events      []Event `json:"events"`
}
type CommandResult struct {
	Task       Task     `json:"task"`
	Records    []Record `json:"records"`
	CommitID   string   `json:"commit_id"`
	LeaseToken string   `json:"lease_token,omitempty"`
}
