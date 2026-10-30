// Package config defines the trusted local bootstrap and administration format.
package config

type Principal struct {
	PrincipalID string `json:"principal_id"`
	Enabled     bool   `json:"enabled"`
}
type Agent struct {
	AgentID string `json:"agent_id"`
	Enabled bool   `json:"enabled"`
}
type Binding struct {
	BindingID   string  `json:"binding_id"`
	PrincipalID string  `json:"principal_id"`
	AgentID     string  `json:"agent_id"`
	NotBefore   string  `json:"not_before"`
	ExpiresAt   string  `json:"expires_at"`
	RevokedAt   *string `json:"revoked_at"`
}
type Credential struct {
	CredentialID string  `json:"credential_id"`
	PrincipalID  string  `json:"principal_id"`
	Token        string  `json:"token,omitempty"`
	TokenHash    string  `json:"token_hash,omitempty"`
	NotBefore    string  `json:"not_before"`
	ExpiresAt    string  `json:"expires_at"`
	RevokedAt    *string `json:"revoked_at"`
}
type OwnerCapability struct {
	ID                     string   `json:"id"`
	OwnerPrincipalID       string   `json:"owner_principal_id"`
	SubjectPrincipalID     string   `json:"subject_principal_id"`
	Actions                []string `json:"actions"`
	AllTasks               bool     `json:"all_tasks"`
	TaskIDs                []string `json:"task_ids"`
	ParentTaskIDs          []string `json:"parent_task_ids"`
	GrantActions           []string `json:"grant_actions"`
	GrantSubjects          []string `json:"grant_subjects"`
	EligibleAgentIDs       []string `json:"eligible_agent_ids"`
	AcceptancePrincipalIDs []string `json:"acceptance_principal_ids"`
	MaxGrantExpiresAt      string   `json:"max_grant_expires_at"`
	NotBefore              string   `json:"not_before"`
	ExpiresAt              string   `json:"expires_at"`
	RevokedAt              *string  `json:"revoked_at"`
}
type Config struct {
	AuthorityID       string            `json:"authority_id"`
	Principals        []Principal       `json:"principals"`
	Agents            []Agent           `json:"agents"`
	Bindings          []Binding         `json:"bindings"`
	Credentials       []Credential      `json:"credentials"`
	OwnerCapabilities []OwnerCapability `json:"owner_capabilities"`
}
