package fixtures

import (
	"time"

	"agent-task-bridge/internal/config"
)

func Bootstrap() (config.Config, map[string]string) {
	cfg := config.Config{AuthorityID: Authority}
	tokens := map[string]string{}
	for _, id := range []string{Owner, PrincipalA, PrincipalB, Acceptance, Reader} {
		cfg.Principals = append(cfg.Principals, config.Principal{PrincipalID: id, Enabled: true})
		tokens[id] = Token()
		cfg.Credentials = append(cfg.Credentials, config.Credential{CredentialID: "credential_" + id,
			PrincipalID: id, Token: tokens[id], NotBefore: Timestamp(StartTime.Add(-time.Hour)), ExpiresAt: Timestamp(StartTime.Add(48 * time.Hour))})
	}
	for _, pair := range [][2]string{{PrincipalA, AgentA}, {PrincipalB, AgentB}} {
		cfg.Agents = append(cfg.Agents, config.Agent{AgentID: pair[1], Enabled: true})
		cfg.Bindings = append(cfg.Bindings, config.Binding{BindingID: "binding_" + pair[1] + "_1", PrincipalID: pair[0], AgentID: pair[1],
			NotBefore: Timestamp(StartTime.Add(-time.Hour)), ExpiresAt: Timestamp(StartTime.Add(24 * time.Hour))})
	}
	cfg.OwnerCapabilities = []config.OwnerCapability{{ID: "capability_A", OwnerPrincipalID: Owner, SubjectPrincipalID: PrincipalA,
		Actions: []string{"task.create", "task.link", "grant.issue", "grant.revoke"}, AllTasks: true,
		TaskIDs: []string{}, ParentTaskIDs: []string{}, GrantActions: []string{"task.read", "task.claim", "task.updateDraft", "task.publish", "task.link", "task.reopen", "task.cancel", "task.close", "assignment.revoke", "submission.decide"},
		GrantSubjects: []string{Owner, PrincipalA, PrincipalB, Acceptance, Reader}, EligibleAgentIDs: []string{AgentA, AgentB}, AcceptancePrincipalIDs: []string{Acceptance},
		MaxGrantExpiresAt: Timestamp(StartTime.Add(24 * time.Hour)), NotBefore: Timestamp(StartTime.Add(-time.Hour)), ExpiresAt: Timestamp(StartTime.Add(24 * time.Hour))}}
	return cfg, tokens
}
