// Package auth evaluates current authority facts without owning transactions.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Csneer/agent-task-bridge/internal/config"
	"github.com/Csneer/agent-task-bridge/internal/domain"
)

const timestampLayout = "2006-01-02T15:04:05.000Z"

var ErrUnauthenticated = errors.New("unauthenticated")

var taskActions = map[string]bool{
	"task.read": true, "task.claim": true, "task.updateDraft": true,
	"task.publish": true, "task.link": true, "task.reopen": true,
	"task.cancel": true, "task.close": true, "assignment.revoke": true,
	"submission.decide": true,
}

// TokenHash returns the stored digest, never the original bearer credential.
func TokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func Authenticate(c config.Config, token string, now time.Time) (string, string, error) {
	if token == "" {
		return "", "", ErrUnauthenticated
	}
	digest := TokenHash(token)
	for _, credential := range c.Credentials {
		if subtle.ConstantTimeCompare([]byte(digest), []byte(credential.TokenHash)) == 1 &&
			valid(credential.NotBefore, credential.ExpiresAt, credential.RevokedAt, now) && principalEnabled(c, credential.PrincipalID) {
			return credential.PrincipalID, credential.CredentialID, nil
		}
	}
	return "", "", ErrUnauthenticated
}

func Binding(c config.Config, principalID, agentID string, now time.Time) *config.Binding {
	for i := range c.Bindings {
		binding := &c.Bindings[i]
		if binding.PrincipalID == principalID && binding.AgentID == agentID && bindingValid(c, *binding, now) {
			return binding
		}
	}
	return nil
}

func BindingByID(c config.Config, id string, now time.Time) *config.Binding {
	for i := range c.Bindings {
		if c.Bindings[i].BindingID == id && bindingValid(c, c.Bindings[i], now) {
			return &c.Bindings[i]
		}
	}
	return nil
}

func bindingValid(c config.Config, binding config.Binding, now time.Time) bool {
	return principalEnabled(c, binding.PrincipalID) && agentEnabled(c, binding.AgentID) &&
		valid(binding.NotBefore, binding.ExpiresAt, binding.RevokedAt, now)
}

// Can checks one action, independently of lifecycle/CAS guards. It returns the
// explicit authorization IDs used for audit. A supplied Agent context must be
// currently bound even when the Principal also has independent Owner rights.
func Can(c config.Config, principalID, agentID string, task domain.Task, grants []domain.Grant, action string, now time.Time) (bool, []string) {
	if !principalEnabled(c, principalID) || (agentID != "" && Binding(c, principalID, agentID, now) == nil) {
		return false, nil
	}
	if action == "submission.decide" && principalID != task.Contract.AcceptancePrincipalID {
		return false, nil
	}
	if action == "task.claim" && (agentID == "" || !contains(task.Contract.EligibleAgentIDs, agentID)) {
		return false, nil
	}
	if principalID == task.Contract.OwnerPrincipalID && action != "task.claim" &&
		(taskActions[action] || action == "grant.issue" || action == "grant.revoke") {
		return true, []string{"owner:" + principalID}
	}
	for _, grant := range grants {
		if grant.TaskID != task.TaskID || grant.OwnerPrincipalID != task.Contract.OwnerPrincipalID ||
			grant.SubjectPrincipalID != principalID || !valid(grant.NotBefore, grant.ExpiresAt, grant.RevokedAt, now) || !contains(grant.Actions, action) {
			continue
		}
		if grant.AgentID != nil && (*grant.AgentID != agentID || Binding(c, principalID, agentID, now) == nil) {
			continue
		}
		if action == "task.claim" && (grant.AgentID == nil || !contains(grant.Actions, "task.read")) {
			continue
		}
		if action == "submission.decide" && (grant.AgentID != nil || !contains(grant.Actions, "task.read")) {
			continue
		}
		if taskActions[action] {
			return true, []string{grant.GrantID}
		}
	}
	if action == "task.claim" || action == "submission.decide" {
		return false, nil
	}
	for _, capability := range c.OwnerCapabilities {
		if capability.SubjectPrincipalID == principalID && capability.OwnerPrincipalID == task.Contract.OwnerPrincipalID &&
			valid(capability.NotBefore, capability.ExpiresAt, capability.RevokedAt, now) && contains(capability.Actions, action) && capabilityScope(capability, task.TaskID, action) {
			return true, []string{capability.ID}
		}
	}
	return false, nil
}

func CanCreate(c config.Config, principalID string, contract domain.Contract, now time.Time) bool {
	allowed, _ := CreateAuthorization(c, principalID, contract, now)
	return allowed
}

func CanIssue(c config.Config, principalID string, task domain.Task, grant domain.Grant, now time.Time) bool {
	allowed, _ := IssueAuthorization(c, principalID, task, grant, now)
	return allowed
}

// CreateAuthorization returns the authority actually used to create a Task.
func CreateAuthorization(c config.Config, principalID string, contract domain.Contract, now time.Time) (bool, []string) {
	if !principalEnabled(c, principalID) || !principalExists(c, contract.OwnerPrincipalID) || !principalExists(c, contract.AcceptancePrincipalID) {
		return false, nil
	}
	for _, agent := range contract.EligibleAgentIDs {
		if !agentExists(c, agent) {
			return false, nil
		}
	}
	if principalID == contract.OwnerPrincipalID {
		return true, []string{"owner:" + principalID}
	}
	for _, capability := range c.OwnerCapabilities {
		if capability.SubjectPrincipalID != principalID || capability.OwnerPrincipalID != contract.OwnerPrincipalID ||
			!valid(capability.NotBefore, capability.ExpiresAt, capability.RevokedAt, now) || !contains(capability.Actions, "task.create") {
			continue
		}
		if subset(contract.EligibleAgentIDs, capability.EligibleAgentIDs) && contains(capability.AcceptancePrincipalIDs, contract.AcceptancePrincipalID) {
			return true, []string{capability.ID}
		}
	}
	return false, nil
}

// IssueAuthorization validates the requested grant and the issuer envelope. The caller
// separately checks current task.read (or atomic creation rights for a new Task).
func IssueAuthorization(c config.Config, principalID string, task domain.Task, grant domain.Grant, now time.Time) (bool, []string) {
	if !principalEnabled(c, principalID) || !principalExists(c, grant.SubjectPrincipalID) ||
		grant.TaskID != task.TaskID || grant.OwnerPrincipalID != task.Contract.OwnerPrincipalID || grant.RevokedAt != nil || len(grant.Actions) == 0 {
		return false, nil
	}
	if _, _, ok := interval(grant.NotBefore, grant.ExpiresAt); !ok {
		return false, nil
	}
	seen := make(map[string]bool)
	for _, action := range grant.Actions {
		if !taskActions[action] || seen[action] {
			return false, nil
		}
		seen[action] = true
	}
	if grant.AgentID != nil && !agentExists(c, *grant.AgentID) {
		return false, nil
	}
	if seen["task.claim"] && (grant.AgentID == nil || !seen["task.read"] || !contains(task.Contract.EligibleAgentIDs, *grant.AgentID)) {
		return false, nil
	}
	if seen["submission.decide"] && (grant.AgentID != nil || !seen["task.read"] || grant.SubjectPrincipalID != task.Contract.AcceptancePrincipalID) {
		return false, nil
	}
	if principalID == task.Contract.OwnerPrincipalID {
		return true, []string{"owner:" + principalID}
	}
	for _, capability := range c.OwnerCapabilities {
		if capability.SubjectPrincipalID != principalID || capability.OwnerPrincipalID != task.Contract.OwnerPrincipalID ||
			!valid(capability.NotBefore, capability.ExpiresAt, capability.RevokedAt, now) || !contains(capability.Actions, "grant.issue") {
			continue
		}
		if !capabilityScope(capability, task.TaskID, "grant.issue") || !subset(grant.Actions, capability.GrantActions) || !contains(capability.GrantSubjects, grant.SubjectPrincipalID) {
			continue
		}
		deadline, err := time.Parse(timestampLayout, capability.MaxGrantExpiresAt)
		grantDeadline, grantErr := time.Parse(timestampLayout, grant.ExpiresAt)
		if err == nil && grantErr == nil && !grantDeadline.After(deadline) {
			return true, []string{capability.ID}
		}
	}
	return false, nil
}

func capabilityScope(capability config.OwnerCapability, taskID, action string) bool {
	if action == "task.link" {
		return contains(capability.ParentTaskIDs, taskID)
	}
	return capability.AllTasks || contains(capability.TaskIDs, taskID)
}

// Horizon conservatively invalidates cursors at the next configured temporal
// boundary, including grants that will become effective in the future.
func Horizon(c config.Config, allGrants []domain.Grant, now time.Time) *string {
	var earliest *time.Time
	consider := func(values ...string) {
		for _, value := range values {
			point, err := time.Parse(timestampLayout, value)
			if err == nil && point.After(now) && (earliest == nil || point.Before(*earliest)) {
				copyPoint := point
				earliest = &copyPoint
			}
		}
	}
	for _, binding := range c.Bindings {
		consider(binding.NotBefore, binding.ExpiresAt)
	}
	for _, credential := range c.Credentials {
		consider(credential.NotBefore, credential.ExpiresAt)
	}
	for _, capability := range c.OwnerCapabilities {
		consider(capability.NotBefore, capability.ExpiresAt)
	}
	for _, grant := range allGrants {
		consider(grant.NotBefore, grant.ExpiresAt)
	}
	if earliest == nil {
		return nil
	}
	formatted := earliest.UTC().Format(timestampLayout)
	return &formatted
}

func valid(start, end string, revoked *string, now time.Time) bool {
	from, until, ok := interval(start, end)
	return ok && revoked == nil && !now.Before(from) && now.Before(until)
}

func interval(start, end string) (time.Time, time.Time, bool) {
	from, err1 := time.Parse(timestampLayout, start)
	until, err2 := time.Parse(timestampLayout, end)
	return from, until, err1 == nil && err2 == nil && from.Year() > 0 && until.Year() > 0 &&
		from.Format(timestampLayout) == start && until.Format(timestampLayout) == end && from.Before(until)
}

func principalEnabled(c config.Config, id string) bool {
	for _, principal := range c.Principals {
		if principal.PrincipalID == id {
			return principal.Enabled
		}
	}
	return false
}

func principalExists(c config.Config, id string) bool {
	for _, principal := range c.Principals {
		if principal.PrincipalID == id {
			return true
		}
	}
	return false
}

func agentEnabled(c config.Config, id string) bool {
	for _, agent := range c.Agents {
		if agent.AgentID == id {
			return agent.Enabled
		}
	}
	return false
}

func agentExists(c config.Config, id string) bool {
	for _, agent := range c.Agents {
		if agent.AgentID == id {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func subset(values, allowed []string) bool {
	for _, value := range values {
		if !contains(allowed, value) {
			return false
		}
	}
	return true
}

func sortConfig(c *config.Config) {
	sort.Slice(c.Principals, func(i, j int) bool { return c.Principals[i].PrincipalID < c.Principals[j].PrincipalID })
	sort.Slice(c.Agents, func(i, j int) bool { return c.Agents[i].AgentID < c.Agents[j].AgentID })
	sort.Slice(c.Bindings, func(i, j int) bool { return c.Bindings[i].BindingID < c.Bindings[j].BindingID })
	sort.Slice(c.Credentials, func(i, j int) bool { return c.Credentials[i].CredentialID < c.Credentials[j].CredentialID })
	sort.Slice(c.OwnerCapabilities, func(i, j int) bool { return c.OwnerCapabilities[i].ID < c.OwnerCapabilities[j].ID })
}

func revokedAtNow(now time.Time) *string {
	value := now.UTC().Format(timestampLayout)
	return &value
}

func safeHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}
