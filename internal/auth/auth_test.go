package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/internal/config"
	"github.com/Csneer/agent-task-bridge/internal/domain"
)

var testNow = time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

const before = "2026-10-29T00:00:00.000Z"
const after = "2026-10-31T00:00:00.000Z"

func ptr(value string) *string { return &value }

func fixture(t *testing.T) config.Config {
	t.Helper()
	c, err := PrepareImport(nil, config.Config{
		AuthorityID: "authority",
		Principals:  []config.Principal{{PrincipalID: "owner", Enabled: true}, {PrincipalID: "alice", Enabled: true}, {PrincipalID: "bob", Enabled: true}, {PrincipalID: "acceptor", Enabled: true}},
		Agents:      []config.Agent{{AgentID: "A", Enabled: true}, {AgentID: "B", Enabled: true}},
		Bindings: []config.Binding{
			{BindingID: "bind_A", PrincipalID: "alice", AgentID: "A", NotBefore: before, ExpiresAt: after},
			{BindingID: "bind_B", PrincipalID: "bob", AgentID: "B", NotBefore: before, ExpiresAt: after},
		},
		Credentials: []config.Credential{
			{CredentialID: "cred_A", PrincipalID: "alice", Token: "credential-A", NotBefore: before, ExpiresAt: after},
			{CredentialID: "cred_B", PrincipalID: "bob", Token: "credential-B", NotBefore: before, ExpiresAt: after},
		},
		OwnerCapabilities: []config.OwnerCapability{{
			ID: "issuer_A", OwnerPrincipalID: "owner", SubjectPrincipalID: "alice",
			Actions:  []string{"task.create", "task.read", "task.link", "task.publish", "grant.issue", "grant.revoke"},
			AllTasks: true, ParentTaskIDs: []string{"parent"}, GrantActions: []string{"task.read", "task.claim", "submission.decide"},
			GrantSubjects: []string{"bob", "acceptor"}, EligibleAgentIDs: []string{"B"}, AcceptancePrincipalIDs: []string{"acceptor"},
			NotBefore: before, ExpiresAt: after, MaxGrantExpiresAt: after,
		}},
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func taskFixture() domain.Task {
	return domain.Task{TaskID: "child", State: "offered", Contract: domain.Contract{
		OwnerPrincipalID: "owner", EligibleAgentIDs: []string{"B"}, AcceptancePrincipalID: "acceptor",
	}}
}

func grantFixture() domain.Grant {
	return domain.Grant{GrantID: "grant_B", TaskID: "child", OwnerPrincipalID: "owner", SubjectPrincipalID: "bob", AgentID: ptr("B"),
		Actions: []string{"task.read", "task.claim"}, NotBefore: before, ExpiresAt: after, IssuedByPrincipalID: "alice"}
}

func clone(c config.Config) config.Config {
	data, _ := json.Marshal(c)
	var copy config.Config
	_ = json.Unmarshal(data, &copy)
	return copy
}

func TestAuthenticationAndSensitiveConfig(t *testing.T) {
	c := fixture(t)
	principal, credential, err := Authenticate(c, "credential-B", testNow)
	if err != nil || principal != "bob" || credential != "cred_B" {
		t.Fatalf("authentication: %s %s %v", principal, credential, err)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "credential-A") || strings.Contains(string(raw), "credential-B") {
		t.Fatal("persistable config contains bearer value")
	}
	if _, _, err := Authenticate(c, "", testNow); err == nil {
		t.Fatal("empty bearer authenticated")
	}
	if _, _, err := Authenticate(c, "credential-B", testNow.Add(24*time.Hour)); err == nil {
		t.Fatal("credential authenticated at exact expiry")
	}
	for i := range c.Credentials {
		if c.Credentials[i].CredentialID == "cred_B" {
			c.Credentials[i].RevokedAt = ptr(testNow.Format(timestampLayout))
		}
	}
	if _, _, err := Authenticate(c, "credential-B", testNow); err == nil {
		t.Fatal("revoked credential authenticated")
	}
	if Binding(c, "bob", "B", testNow) == nil {
		t.Fatal("credential revocation incorrectly terminated binding")
	}
}

func TestOwnerAndAssignmentPermissionsAreIndependent(t *testing.T) {
	c, task, grant := fixture(t), taskFixture(), grantFixture()
	checks := []struct {
		principal, agent, action string
		want                     bool
	}{
		{"owner", "", "task.read", true}, {"owner", "", "task.claim", false},
		{"owner", "", "submission.decide", false}, {"bob", "B", "task.claim", true},
		{"bob", "A", "task.claim", false}, {"bob", "", "task.read", false},
		{"bob", "B", "grant.issue", false}, {"alice", "A", "task.publish", true},
		{"alice", "B", "task.publish", false},
	}
	for _, check := range checks {
		got, _ := Can(c, check.principal, check.agent, task, []domain.Grant{grant}, check.action, testNow)
		if got != check.want {
			t.Errorf("%+v: got %v", check, got)
		}
	}
	task.Contract.AcceptancePrincipalID = "owner"
	if got, _ := Can(c, "owner", "", task, nil, "submission.decide", testNow); !got {
		t.Fatal("designated Owner required an extra acceptance grant")
	}
}

func TestDirectGrantSurvivesIssuerRevocation(t *testing.T) {
	c, task, grant := fixture(t), taskFixture(), grantFixture()
	c.OwnerCapabilities[0].RevokedAt = ptr(testNow.Format(timestampLayout))
	if got, _ := Can(c, "bob", "B", task, []domain.Grant{grant}, "task.claim", testNow); !got {
		t.Fatal("issuer revocation cascaded to directly issued grant")
	}
	if CanCreate(c, "alice", task.Contract, testNow) {
		t.Fatal("revoked issuer can still create")
	}
	grant.RevokedAt = ptr(testNow.Format(timestampLayout))
	if got, _ := Can(c, "bob", "B", task, []domain.Grant{grant}, "task.claim", testNow); got {
		t.Fatal("revoked grant usable")
	}
}

func TestCreationAndGrantEnvelope(t *testing.T) {
	c, task, grant := fixture(t), taskFixture(), grantFixture()
	if !CanCreate(c, "alice", task.Contract, testNow) || !CanIssue(c, "alice", task, grant, testNow) {
		t.Fatal("valid explicit delegation rejected")
	}
	parent := task
	parent.TaskID = "parent"
	if got, _ := Can(c, "alice", "A", parent, nil, "task.link", testNow); !got {
		t.Fatal("explicit parent scope rejected")
	}
	parent.TaskID = "other"
	if got, _ := Can(c, "alice", "A", parent, nil, "task.link", testNow); got {
		t.Fatal("all_tasks expanded explicit parent scope")
	}
	badTask := task
	badTask.Contract.EligibleAgentIDs = []string{"A"}
	if CanCreate(c, "alice", badTask.Contract, testNow) {
		t.Fatal("creator enlarged eligible set")
	}
	mutations := []func(*domain.Grant){
		func(g *domain.Grant) { g.Actions = []string{"task.claim"} },
		func(g *domain.Grant) { g.Actions = []string{"grant.issue"} },
		func(g *domain.Grant) { g.AgentID = ptr("A") },
		func(g *domain.Grant) { g.AgentID = nil },
		func(g *domain.Grant) { g.SubjectPrincipalID = "alice" },
		func(g *domain.Grant) { g.ExpiresAt = "2026-11-01T00:00:00.000Z" },
		func(g *domain.Grant) { g.TaskID = "wrong" },
		func(g *domain.Grant) { g.Actions = []string{"task.read", "submission.decide"} },
	}
	for i, mutate := range mutations {
		bad := grantFixture()
		mutate(&bad)
		if CanIssue(c, "alice", task, bad, testNow) {
			t.Errorf("accepted out-of-envelope grant mutation %d", i)
		}
	}
	acceptance := grantFixture()
	acceptance.SubjectPrincipalID = "acceptor"
	acceptance.AgentID = nil
	acceptance.Actions = []string{"task.read", "submission.decide"}
	if !CanIssue(c, "alice", task, acceptance, testNow) {
		t.Fatal("valid acceptance grant rejected")
	}
	if got, _ := Can(c, "acceptor", "", task, []domain.Grant{acceptance}, "submission.decide", testNow); !got {
		t.Fatal("designated independent acceptance rejected")
	}
}

func TestDisableReenableDoesNotRestoreBinding(t *testing.T) {
	initial := fixture(t)
	desired := clone(initial)
	for i := range desired.Agents {
		if desired.Agents[i].AgentID == "B" {
			desired.Agents[i].Enabled = false
		}
	}
	disabled, err := PrepareImport(&initial, desired, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if BindingByID(disabled, "bind_B", testNow) != nil {
		t.Fatal("disabled binding usable")
	}
	if BindingByID(initial, "bind_B", testNow) == nil {
		t.Fatal("PrepareImport mutated previous config")
	}
	reenabled := clone(disabled)
	for i := range reenabled.Agents {
		reenabled.Agents[i].Enabled = true
	}
	reenabled.Bindings = append(reenabled.Bindings, config.Binding{BindingID: "bind_B_2", PrincipalID: "bob", AgentID: "B", NotBefore: before, ExpiresAt: after})
	result, err := PrepareImport(&disabled, reenabled, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if BindingByID(result, "bind_B", testNow) != nil || BindingByID(result, "bind_B_2", testNow) == nil {
		t.Fatal("reenabling did not preserve old instance fence")
	}
	if _, err := PrepareImport(&result, initial, testNow); err == nil {
		t.Fatal("old bootstrap config restored revoked binding ID")
	}
}

func TestOmittedIdentitiesRemainTombstoned(t *testing.T) {
	initial := fixture(t)
	desired := config.Config{AuthorityID: initial.AuthorityID}
	result, err := PrepareImport(&initial, desired, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Principals) != len(initial.Principals) || len(result.Bindings) != len(initial.Bindings) || len(result.Credentials) != len(initial.Credentials) {
		t.Fatal("import removed historical authorization identities")
	}
	for _, binding := range result.Bindings {
		if binding.RevokedAt == nil {
			t.Fatal("omitted binding not revoked")
		}
	}
	for _, credential := range result.Credentials {
		if credential.RevokedAt == nil || credential.Token != "" {
			t.Fatal("omitted credential not safely tombstoned")
		}
	}
	if _, err := PrepareImport(&result, initial, testNow); err == nil {
		t.Fatal("omitted historical instances can be resurrected")
	}
}

func TestImportRejectsIdentityMutationAndOverlap(t *testing.T) {
	initial := fixture(t)
	mutations := []func(*config.Config){
		func(c *config.Config) { c.AuthorityID = "another" },
		func(c *config.Config) { c.Bindings[0].ExpiresAt = "2026-11-01T00:00:00.000Z" },
		func(c *config.Config) { c.Bindings[0].AgentID = "B" },
		func(c *config.Config) {
			c.Bindings = append(c.Bindings, config.Binding{BindingID: "overlap", PrincipalID: "alice", AgentID: "A", NotBefore: "2026-10-30T01:00:00.000Z", ExpiresAt: after})
		},
		func(c *config.Config) { c.Credentials[0].Token = "replacement" },
		func(c *config.Config) { c.Credentials[0].PrincipalID = "owner" },
		func(c *config.Config) { c.Credentials[0].CredentialID = "reassigned" },
		func(c *config.Config) { c.Principals = append(c.Principals, c.Principals[0]) },
		func(c *config.Config) { c.OwnerCapabilities[0].GrantActions = []string{"grant.issue"} },
	}
	for i, mutate := range mutations {
		bad := clone(initial)
		mutate(&bad)
		if _, err := PrepareImport(&initial, bad, testNow); err == nil {
			t.Errorf("accepted invalid import mutation %d", i)
		}
	}
}

func TestCredentialRevocationDoesNotRevokeOtherCredential(t *testing.T) {
	initial := fixture(t)
	desired := clone(initial)
	desired.Credentials = append(desired.Credentials, config.Credential{CredentialID: "cred_B2", PrincipalID: "bob", Token: "other-bearer", NotBefore: before, ExpiresAt: after})
	for i := range desired.Credentials {
		if desired.Credentials[i].CredentialID == "cred_B" {
			desired.Credentials[i].RevokedAt = ptr(testNow.Format(timestampLayout))
		}
	}
	result, err := PrepareImport(&initial, desired, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Authenticate(result, "credential-B", testNow); err == nil {
		t.Fatal("revoked credential usable")
	}
	if _, _, err := Authenticate(result, "other-bearer", testNow); err != nil {
		t.Fatal("independent valid credential affected")
	}
	if BindingByID(result, "bind_B", testNow) == nil {
		t.Fatal("individual credential revoke ended Assignment binding")
	}
}

func TestHorizonIncludesFutureActivation(t *testing.T) {
	c := fixture(t)
	grant := grantFixture()
	grant.NotBefore = "2026-10-30T00:01:00.000Z"
	horizon := Horizon(c, []domain.Grant{grant}, testNow)
	if horizon == nil || *horizon != grant.NotBefore {
		t.Fatalf("wrong future activation horizon: %v", horizon)
	}
	point, _ := time.Parse(timestampLayout, grant.NotBefore)
	horizon = Horizon(c, []domain.Grant{grant}, point)
	if horizon == nil || *horizon != after {
		t.Fatalf("boundary failed to advance: %v", horizon)
	}
	if Horizon(config.Config{}, nil, testNow) != nil {
		t.Fatal("empty authorization context has finite horizon")
	}
}

func TestCreationAndIssuanceAuditAuthority(t *testing.T) {
	c, task, grant := fixture(t), taskFixture(), grantFixture()
	for _, principal := range []string{"owner", "alice", "bob"} {
		want := ""
		if principal == "owner" {
			want = "owner:owner"
		}
		if principal == "alice" {
			want = "issuer_A"
		}
		create, createIDs := CreateAuthorization(c, principal, task.Contract, testNow)
		issue, issueIDs := IssueAuthorization(c, principal, task, grant, testNow)
		for label, result := range map[string]struct {
			allowed bool
			ids     []string
		}{
			"create": {create, createIDs}, "issue": {issue, issueIDs},
		} {
			if result.allowed != (want != "") || (want == "" && len(result.ids) != 0) || (want != "" && (len(result.ids) != 1 || result.ids[0] != want)) {
				t.Errorf("%s %s: allowed=%v ids=%v want=%s", principal, label, result.allowed, result.ids, want)
			}
		}
	}
	c.OwnerCapabilities[0].RevokedAt = ptr(testNow.Format(timestampLayout))
	if ok, ids := CreateAuthorization(c, "alice", task.Contract, testNow); ok || len(ids) != 0 {
		t.Fatalf("revoked creator authority: %v %v", ok, ids)
	}
	if ok, ids := IssueAuthorization(c, "alice", task, grant, testNow); ok || len(ids) != 0 {
		t.Fatalf("revoked issuer authority: %v %v", ok, ids)
	}
}
