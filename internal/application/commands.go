package application

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"agent-task-bridge/internal/auth"
	"agent-task-bridge/internal/config"
	"agent-task-bridge/internal/domain"
	"agent-task-bridge/internal/ports"
	"agent-task-bridge/internal/secrets"
	"agent-task-bridge/internal/wirevalue"
)

var writeMethods = map[string]bool{"task.create": true, "task.createChild": true, "task.updateDraft": true, "task.publish": true, "task.claim": true, "assignment.renew": true, "assignment.release": true, "assignment.revoke": true, "run.start": true, "run.report": true, "run.submit": true, "task.accept": true, "task.reject": true, "task.reopen": true, "task.cancel": true, "task.close": true, "grant.issue": true, "grant.revoke": true}

func execution(method string) bool {
	return method == "assignment.renew" || method == "assignment.release" || strings.HasPrefix(method, "run.")
}

func (s *Service) recover(tx ports.Transaction, c config.Config, p, agent, key, hash string, now time.Time) (*domain.CommandResult, error) {
	receipt, e := tx.Receipt(p, key)
	if e != nil || receipt == nil {
		return nil, e
	}
	if hash != "" && receipt.Hash != hash {
		return nil, domain.E("IDEMPOTENCY_CONFLICT")
	}
	task, e := tx.Task(receipt.TaskID)
	if e != nil {
		return nil, e
	}
	gs, e := grants(tx, receipt.TaskID)
	if e != nil {
		return nil, e
	}
	if e = requireVisible(c, p, agent, task, gs, now); e != nil {
		return nil, e
	}
	if receipt.Encrypted && auth.Binding(c, p, receipt.AgentID, now) == nil {
		return nil, domain.E("FORBIDDEN")
	}
	raw := receipt.Payload
	if receipt.Encrypted {
		raw, e = s.box.Open(raw, p+":"+key)
		if e != nil {
			return nil, domain.E("RECEIPT_UNAVAILABLE")
		}
	}
	var result domain.CommandResult
	if e = json.Unmarshal(raw, &result); e != nil {
		return nil, e
	}
	return &result, nil
}

func (s *Service) command(ctx context.Context, token, requestID, method string, params domain.Object) (any, bool, error) {
	key, agent, taskID := domain.S(params, "idempotency_key"), domain.S(params, "as_agent_id"), domain.S(params, "task_id")
	hash, e := wirevalue.RequestHash(method, map[string]any(params))
	if e != nil {
		return nil, false, e
	}
	// This probe never holds a lock over the independent maintenance transaction.
	tx, e := s.store.Begin(ctx, true)
	if e != nil {
		return nil, false, e
	}
	now, e := s.now(tx, false)
	if e != nil {
		tx.Rollback()
		return nil, false, e
	}
	c, e := loadConfig(tx)
	if e != nil {
		tx.Rollback()
		return nil, false, e
	}
	p, e := authn(c, token, now)
	if e != nil {
		tx.Rollback()
		return nil, false, e
	}
	if taskID != "" {
		task, er := tx.Task(taskID)
		if er == nil {
			var gs []domain.Grant
			gs, er = grants(tx, taskID)
			if er == nil {
				er = requireVisible(c, p, agent, task, gs, now)
			}
		}
		if er != nil {
			tx.Rollback()
			return nil, false, er
		}
	}
	recovered, e := s.recover(tx, c, p, agent, key, hash, now)
	tx.Rollback()
	if e != nil {
		return nil, false, e
	}
	if recovered != nil {
		return recovered, true, nil
	}
	if taskID != "" {
		if e = s.maintainTask(ctx, taskID, token, agent); e != nil {
			return nil, false, e
		}
	}
	if e = s.point("before_command_tx", method, key); e != nil {
		return nil, false, e
	}
	tx, e = s.store.Begin(ctx, true)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback()
	now, e = s.now(tx, true)
	if e != nil {
		return nil, false, e
	}
	c, e = loadConfig(tx)
	if e != nil {
		return nil, false, e
	}
	p, e = authn(c, token, now)
	if e != nil {
		return nil, false, e
	}
	recovered, e = s.recover(tx, c, p, agent, key, hash, now)
	if e != nil {
		return nil, false, e
	}
	if recovered != nil {
		return recovered, true, nil
	}
	if agent != "" && auth.Binding(c, p, agent, now) == nil {
		return nil, false, domain.E("FORBIDDEN")
	}
	var task *domain.Task
	var gs []domain.Grant
	if taskID != "" {
		task, e = tx.Task(taskID)
		if e != nil {
			return nil, false, e
		}
		gs, e = grants(tx, taskID)
		if e != nil {
			return nil, false, e
		}
		if e = requireVisible(c, p, agent, task, gs, now); e != nil {
			return nil, false, e
		}
	}
	if task == nil {
		return s.createCommand(tx, c, p, agent, requestID, method, key, hash, params, now)
	}
	// Final authorization is checked against authoritative facts before CAS.
	action := method
	if method == "task.accept" || method == "task.reject" {
		action = "submission.decide"
	}
	var a *domain.Assignment
	var used []string
	if execution(method) {
		a, e = typedRecord[domain.Assignment](tx, "Assignment", domain.S(domain.O(params, "lease"), "assignment_id"))
		if e != nil {
			return nil, false, e
		}
		if a != nil && a.TaskID == task.TaskID {
			var g *domain.Grant
			g, e = typedRecord[domain.Grant](tx, "Grant", a.ClaimGrantID)
			if e != nil {
				return nil, false, e
			}
			if g == nil || !allowed(c, p, agent, *task, []domain.Grant{*g}, "task.claim", now) || auth.BindingByID(c, a.BindingID, now) == nil {
				return nil, false, domain.E("FORBIDDEN")
			}
			used = []string{g.GrantID, a.BindingID}
		}
	} else if method == "task.claim" {
		g, e := typedRecord[domain.Grant](tx, "Grant", domain.S(params, "claim_grant_id"))
		if e != nil {
			return nil, false, e
		}
		if g == nil || g.TaskID != task.TaskID || !allowed(c, p, agent, *task, []domain.Grant{*g}, "task.claim", now) {
			return nil, false, domain.E("FORBIDDEN")
		}
		used = []string{g.GrantID, auth.Binding(c, p, agent, now).BindingID}
	} else {
		var ok bool
		ok, used = auth.Can(c, p, agent, *task, gs, action, now)
		if !ok {
			return nil, false, domain.E("FORBIDDEN")
		}
	}
	if (method == "task.accept" || method == "task.reject") && task.Contract.AcceptancePrincipalID != p {
		return nil, false, domain.E("FORBIDDEN")
	}
	if e = revision(task, domain.N(params, "expected_revision")); e != nil {
		return nil, false, e
	}
	if execution(method) {
		lease := domain.O(params, "lease")
		stored := ""
		if a != nil {
			stored, e = tx.TokenHash(a.AssignmentID)
			if e != nil {
				return nil, false, e
			}
		}
		if a == nil || a.TaskID != task.TaskID || !live(a) || a.PrincipalID != p || a.AgentID != agent || domain.String(task.CurrentAssignmentID) != a.AssignmentID || a.Generation != domain.N(lease, "generation") || a.Generation != task.Generation || !now.Before(domain.ParseTime(a.LeaseExpiresAt)) || subtle.ConstantTimeCompare([]byte(stored), []byte(secrets.Hash(domain.S(lease, "token")))) != 1 {
			tx.Rollback()
			_ = s.maintainTask(ctx, task.TaskID, "", "")
			return nil, false, domain.E("LEASE_FENCED")
		}
	}
	previous := task.Revision
	task.Revision++
	task.UpdatedAt = domain.Stamp(now)
	events := []domain.Event{}
	changed := []domain.Record{}
	claimToken := ""
	emit := func(kind, eventType string, value any) {
		r := domain.RecordOf(kind, value)
		events = append(events, domain.Event{EventID: domain.NewID("evt"), Type: eventType, TaskID: task.TaskID, TaskRevision: task.Revision, RecordType: kind, Record: r.Record})
		if kind != "Task" {
			changed = append(changed, r)
		}
	}
	taskEvent := "task.updated"
	switch method {
	case "task.updateDraft":
		if task.State != "draft" {
			return nil, false, domain.E("INVALID_STATE")
		}
		contract, e := domain.Decode[domain.Contract](params["contract"])
		if e != nil {
			return nil, false, e
		}
		if contract.OwnerPrincipalID != task.Contract.OwnerPrincipalID {
			return nil, false, domain.E("POLICY_DENIED")
		}
		if e = validateContract(c, contract); e != nil {
			return nil, false, e
		}
		if e = checkDraftBoundary(tx, task.TaskID, contract); e != nil {
			return nil, false, e
		}
		task.Contract = contract
		taskEvent = "task.draft_updated"
	case "task.publish":
		if task.State != "draft" {
			return nil, false, domain.E("INVALID_STATE")
		}
		if e = validateContract(c, task.Contract); e != nil {
			return nil, false, e
		}
		raw, e := wirevalue.Canonical(task.Contract)
		if e != nil {
			return nil, false, e
		}
		digest := secrets.Hash(string(raw))
		task.ContractDigest = &digest
		task.ContentVersion = domain.Ptr(int64(1))
		task.State = "offered"
		task.RecoveryGate = "claimable"
		taskEvent = "task.published"
	case "task.claim":
		if task.State != "offered" {
			return nil, false, domain.E("INVALID_STATE")
		}
		if task.RecoveryGate != "claimable" {
			return nil, false, domain.E("GATE_CLOSED")
		}
		if task.CurrentAssignmentID != nil {
			return nil, false, domain.E("INVALID_STATE")
		}
		if task.Generation >= domain.MaxInteger {
			return nil, false, domain.E("RESOURCE_LIMIT")
		}
		g, e := typedRecord[domain.Grant](tx, "Grant", domain.S(params, "claim_grant_id"))
		if e != nil {
			return nil, false, e
		}
		binding := auth.Binding(c, p, agent, now)
		if binding == nil || g == nil {
			return nil, false, domain.E("FORBIDDEN")
		}
		end, e := leaseEnd(task.Contract, *g, *binding, domain.N(params, "lease_seconds"), now)
		if e != nil {
			return nil, false, e
		}
		if end.Sub(now) < time.Duration(task.Contract.LeasePolicy.MinSeconds)*time.Second {
			return nil, false, domain.E("POLICY_DENIED")
		}
		task.Generation++
		aid := domain.NewID("asg")
		a = &domain.Assignment{AssignmentID: aid, TaskID: task.TaskID, PrincipalID: p, AgentID: agent, BindingID: binding.BindingID, ClaimGrantID: g.GrantID, State: "claimed", Generation: task.Generation, LeaseExpiresAt: domain.Stamp(end), CreatedAt: domain.Stamp(now)}
		task.CurrentAssignmentID = &aid
		task.State = "claimed"
		taskEvent = "task.claimed"
		claimToken, e = secrets.Token()
		if e != nil {
			return nil, false, e
		}
		if e = tx.PutTokenHash(aid, secrets.Hash(claimToken)); e != nil {
			return nil, false, e
		}
		emit("Assignment", "assignment.claimed", a)
	case "assignment.renew":
		if (task.State != "claimed" && task.State != "active") || !live(a) {
			return nil, false, domain.E("INVALID_STATE")
		}
		g, e := typedRecord[domain.Grant](tx, "Grant", a.ClaimGrantID)
		if e != nil {
			return nil, false, e
		}
		binding := auth.BindingByID(c, a.BindingID, now)
		if g == nil || binding == nil {
			return nil, false, domain.E("FORBIDDEN")
		}
		end, e := leaseEnd(task.Contract, *g, *binding, domain.N(params, "lease_seconds"), now)
		if e != nil {
			return nil, false, e
		}
		if !end.After(domain.ParseTime(a.LeaseExpiresAt)) {
			return nil, false, domain.E("POLICY_DENIED")
		}
		a.LeaseExpiresAt = domain.Stamp(end)
		emit("Assignment", "assignment.renewed", a)
	case "run.start":
		if task.State != "claimed" || a.State != "claimed" {
			return nil, false, domain.E("INVALID_STATE")
		}
		r, e := findRun(tx, task.TaskID, a.AssignmentID)
		if e != nil {
			return nil, false, e
		}
		if r != nil {
			return nil, false, domain.E("INVALID_STATE")
		}
		r = &domain.Run{RunID: domain.NewID("run"), TaskID: task.TaskID, AssignmentID: a.AssignmentID, State: "running", StartedAt: domain.Stamp(now)}
		task.State = "active"
		a.State = "active"
		emit("Run", "run.started", r)
		emit("Assignment", "assignment.activated", a)
		taskEvent = "task.activated"
	case "run.report", "run.submit":
		r, e := typedRecord[domain.Run](tx, "Run", domain.S(params, "run_id"))
		if e != nil {
			return nil, false, e
		}
		if task.State != "active" || a.State != "active" || r == nil || r.TaskID != task.TaskID || r.AssignmentID != a.AssignmentID || r.State != "running" {
			return nil, false, domain.E("INVALID_STATE")
		}
		evidence, _ := domain.Decode[[]domain.Object](params["evidence_refs"])
		extensions, _ := domain.Decode[domain.Object](params["extensions"])
		if method == "run.report" {
			report := domain.Report{ReportID: domain.NewID("rpt"), TaskID: task.TaskID, RunID: r.RunID, AssignmentID: a.AssignmentID, Message: domain.S(params, "message"), EvidenceRefs: evidence, Extensions: extensions, ReportedAt: domain.Stamp(now)}
			emit("Report", "run.reported", report)
		} else {
			if task.ContentVersion == nil || domain.N(params, "content_version") != *task.ContentVersion {
				return nil, false, domain.E("POLICY_DENIED")
			}
			artifacts, _ := domain.Decode[[]domain.Object](params["artifact_refs"])
			sub := domain.Submission{SubmissionID: domain.NewID("sub"), TaskID: task.TaskID, RunID: r.RunID, AssignmentID: a.AssignmentID, ContentVersion: *task.ContentVersion, Summary: domain.S(params, "summary"), ArtifactRefs: artifacts, EvidenceRefs: evidence, Extensions: extensions, SubmittedAt: domain.Stamp(now)}
			r.State = "submitted"
			r.EndedAt = domain.Ptr(domain.Stamp(now))
			r.EndReason = domain.Ptr("submitted")
			a.State = "completed"
			a.EndedAt = domain.Ptr(domain.Stamp(now))
			a.EndReason = domain.Ptr("submitted")
			task.CurrentAssignmentID = nil
			task.PendingSubmissionID = &sub.SubmissionID
			task.State = "submitted"
			taskEvent = "task.submitted"
			emit("Submission", "submission.created", sub)
			emit("Run", "run.submitted", r)
			emit("Assignment", "assignment.completed", a)
		}
	case "task.accept", "task.reject":
		if task.State != "submitted" || domain.S(params, "submission_id") != domain.String(task.PendingSubmissionID) {
			return nil, false, domain.E("INVALID_STATE")
		}
		outcome := "accepted"
		if method == "task.reject" {
			outcome = "rejected"
		}
		evidence, _ := domain.Decode[[]domain.Object](params["evidence_refs"])
		decision := domain.Decision{DecisionID: domain.NewID("dec"), TaskID: task.TaskID, SubmissionID: *task.PendingSubmissionID, Outcome: outcome, PrincipalID: p, Reason: domain.S(params, "reason"), EvidenceRefs: evidence, DecidedAt: domain.Stamp(now)}
		task.State = outcome
		task.PendingSubmissionID = nil
		emit("Decision", "decision.created", decision)
		taskEvent = "task." + outcome
	case "assignment.release", "assignment.revoke":
		if task.State != "claimed" && task.State != "active" {
			return nil, false, domain.E("INVALID_STATE")
		}
		if method == "assignment.revoke" {
			if domain.S(params, "assignment_id") != domain.String(task.CurrentAssignmentID) {
				return nil, false, domain.E("INVALID_STATE")
			}
			a, e = typedRecord[domain.Assignment](tx, "Assignment", domain.String(task.CurrentAssignmentID))
			if e != nil {
				return nil, false, e
			}
		}
		state, reason := "released", "released"
		if method == "assignment.revoke" {
			state, reason = "revoked", "revoked"
		}
		if e = endAssignment(tx, task, a, state, reason, now, emit); e != nil {
			return nil, false, e
		}
		taskEvent = "task.reopened"
	case "task.reopen":
		if task.State == "rejected" {
			if !task.Contract.AllowRework {
				return nil, false, domain.E("POLICY_DENIED")
			}
		} else if task.State != "offered" || task.RecoveryGate != "reopen_required" {
			return nil, false, domain.E("INVALID_STATE")
		}
		task.State = "offered"
		task.RecoveryGate = "claimable"
		task.PendingSubmissionID = nil
		taskEvent = "task.reopened"
	case "task.cancel":
		if !domain.Contains([]string{"draft", "offered", "claimed", "active", "submitted"}, task.State) {
			return nil, false, domain.E("INVALID_STATE")
		}
		if task.CurrentAssignmentID != nil {
			a, e = typedRecord[domain.Assignment](tx, "Assignment", *task.CurrentAssignmentID)
			if e != nil {
				return nil, false, e
			}
			if e = endAssignment(tx, task, a, "cancelled", "task_cancelled", now, emit); e != nil {
				return nil, false, e
			}
		}
		task.State = "cancelled"
		task.PendingSubmissionID = nil
		task.CurrentAssignmentID = nil
		taskEvent = "task.cancelled"
	case "task.close":
		if task.State != "accepted" && task.State != "rejected" {
			return nil, false, domain.E("INVALID_STATE")
		}
		task.State = "closed"
		taskEvent = "task.closed"
	case "grant.issue":
		g, e := newGrant(params, *task, p)
		if e != nil {
			return nil, false, e
		}
		ok, issuedWith := auth.IssueAuthorization(c, p, *task, g, now)
		if !ok {
			return nil, false, domain.E("POLICY_DENIED")
		}
		used = append(used, issuedWith...)
		emit("Grant", "grant.issued", g)
		if e = bumpEpoch(tx); e != nil {
			return nil, false, e
		}
	case "grant.revoke":
		g, e := typedRecord[domain.Grant](tx, "Grant", domain.S(params, "grant_id"))
		if e != nil {
			return nil, false, e
		}
		if g == nil || g.TaskID != task.TaskID || g.RevokedAt != nil {
			return nil, false, domain.E("INVALID_STATE")
		}
		g.RevokedAt = domain.Ptr(domain.Stamp(now))
		emit("Grant", "grant.revoked", g)
		if e = bumpEpoch(tx); e != nil {
			return nil, false, e
		}
		if task.CurrentAssignmentID != nil {
			a, e = typedRecord[domain.Assignment](tx, "Assignment", *task.CurrentAssignmentID)
			if e != nil {
				return nil, false, e
			}
			if live(a) && a.ClaimGrantID == g.GrantID {
				if e = endAssignment(tx, task, a, "revoked", "revoked", now, emit); e != nil {
					return nil, false, e
				}
				taskEvent = "task.reopened"
			}
		}
	default:
		return nil, false, domain.E("METHOD_NOT_FOUND")
	}
	emit("Task", taskEvent, task)
	return s.persist(tx, used, p, agent, requestID, method, key, hash, *task, previous, changed, events, claimToken, params, now)
}

func (s *Service) createCommand(tx ports.Transaction, c config.Config, p, agent, requestID, method, key, hash string, params domain.Object, now time.Time) (any, bool, error) {
	if method != "task.create" && method != "task.createChild" {
		return nil, false, domain.E("NOT_FOUND")
	}
	contract, e := domain.Decode[domain.Contract](params["contract"])
	if e != nil {
		return nil, false, e
	}
	if e = validateContract(c, contract); e != nil {
		return nil, false, e
	}
	ok, used := auth.CreateAuthorization(c, p, contract, now)
	if !ok {
		return nil, false, domain.E("FORBIDDEN")
	}
	task := domain.Task{TaskID: domain.NewID("tsk"), State: "draft", Revision: 1, Contract: contract, RecoveryGate: "claimable", CreatedAt: domain.Stamp(now), UpdatedAt: domain.Stamp(now)}
	if e = saveDraftBoundary(tx, c, used, task.TaskID, p == contract.OwnerPrincipalID); e != nil {
		return nil, false, e
	}
	changes := []domain.Record{}
	events := []domain.Event{}
	emit := func(kind, event string, v any) {
		r := domain.RecordOf(kind, v)
		events = append(events, domain.Event{EventID: domain.NewID("evt"), Type: event, TaskID: task.TaskID, TaskRevision: 1, RecordType: kind, Record: r.Record})
		if kind != "Task" {
			changes = append(changes, r)
		}
	}
	emit("Task", "task.created", task)
	if method == "task.createChild" {
		parent, e := tx.Task(domain.S(params, "parent_task_id"))
		if e != nil {
			return nil, false, e
		}
		gs, e := grants(tx, domain.S(params, "parent_task_id"))
		if e != nil {
			return nil, false, e
		}
		if e = requireVisible(c, p, agent, parent, gs, now); e != nil {
			return nil, false, e
		}
		ok, linkedWith := auth.Can(c, p, agent, *parent, gs, "task.link", now)
		if parent.Contract.OwnerPrincipalID != contract.OwnerPrincipalID || !ok {
			return nil, false, domain.E("FORBIDDEN")
		}
		used = append(used, linkedWith...)
		emit("Relation", "relation.created", domain.Relation{RelationID: domain.NewID("rel"), TaskID: task.TaskID, Kind: "parent_child", ParentTaskID: parent.TaskID, CreatedAt: domain.Stamp(now)})
	}
	inputs, _ := domain.Decode[[]domain.Object](params["initial_grants"])
	newGrants := []domain.Grant{}
	for _, input := range inputs {
		g, e := newGrant(input, task, p)
		if e != nil {
			return nil, false, e
		}
		ok, issuedWith := auth.IssueAuthorization(c, p, task, g, now)
		if !ok {
			return nil, false, domain.E("POLICY_DENIED")
		}
		used = append(used, issuedWith...)
		newGrants = append(newGrants, g)
	}
	sort.Slice(newGrants, func(i, j int) bool { return newGrants[i].GrantID < newGrants[j].GrantID })
	for _, g := range newGrants {
		emit("Grant", "grant.issued", g)
	}
	if len(newGrants) > 0 {
		if e = bumpEpoch(tx); e != nil {
			return nil, false, e
		}
	}
	return s.persist(tx, used, p, agent, requestID, method, key, hash, task, 0, changes, events, "", params, now)
}

func newGrant(input domain.Object, task domain.Task, p string) (domain.Grant, error) {
	g, e := domain.Decode[domain.Grant](input)
	g.GrantID = domain.NewID("grt")
	g.TaskID = task.TaskID
	g.OwnerPrincipalID = task.Contract.OwnerPrincipalID
	g.IssuedByPrincipalID = p
	g.RevokedAt = nil
	return g, e
}
func leaseEnd(c domain.Contract, g domain.Grant, b config.Binding, seconds int64, now time.Time) (time.Time, error) {
	if seconds < c.LeasePolicy.MinSeconds || seconds > c.LeasePolicy.MaxSeconds {
		return time.Time{}, domain.E("POLICY_DENIED")
	}
	end := now.Add(time.Duration(seconds) * time.Second)
	for _, limit := range []string{g.ExpiresAt, b.ExpiresAt} {
		if t := domain.ParseTime(limit); t.Before(end) {
			end = t
		}
	}
	return end, nil
}
func validateContract(c config.Config, contract domain.Contract) error {
	if contract.LeasePolicy.MinSeconds > contract.LeasePolicy.MaxSeconds {
		return domain.E("POLICY_DENIED")
	}
	registered := func(id string) bool {
		for _, p := range c.Principals {
			if p.PrincipalID == id && p.Enabled {
				return true
			}
		}
		return false
	}
	if !registered(contract.OwnerPrincipalID) || !registered(contract.AcceptancePrincipalID) {
		return domain.E("POLICY_DENIED")
	}
	for _, id := range contract.EligibleAgentIDs {
		found := false
		for _, a := range c.Agents {
			if a.AgentID == id && a.Enabled {
				found = true
			}
		}
		if !found {
			return domain.E("POLICY_DENIED")
		}
	}
	return nil
}

func endAssignment(tx ports.Transaction, task *domain.Task, a *domain.Assignment, state, reason string, now time.Time, emit func(string, string, any)) error {
	if !live(a) {
		return domain.E("INVALID_STATE")
	}
	a.State = state
	a.EndedAt = domain.Ptr(domain.Stamp(now))
	a.EndReason = &reason
	if state == "expired" {
		a.EffectiveAt = &a.LeaseExpiresAt
	} else {
		a.EffectiveAt = nil
	}
	emit("Assignment", "assignment."+state, a)
	run, e := findRun(tx, task.TaskID, a.AssignmentID)
	if e != nil {
		return e
	}
	if run != nil && run.State == "running" {
		run.State = "ended"
		run.EndedAt = domain.Ptr(domain.Stamp(now))
		run.EndReason = &reason
		emit("Run", "run.ended", run)
	}
	task.CurrentAssignmentID = nil
	task.State = "offered"
	task.RecoveryGate = "reopen_required"
	if task.Contract.RecoveryPolicy == "safe_reopen" {
		task.RecoveryGate = "claimable"
	}
	return nil
}

func (s *Service) persist(tx ports.Transaction, used []string, p, agent, requestID, method, key, hash string, task domain.Task, previous int64, records []domain.Record, events []domain.Event, token string, params domain.Object, now time.Time) (any, bool, error) {
	if e := maintenanceRoom(task); e != nil {
		return nil, false, e
	}
	unique := []string{}
	for _, id := range used {
		if !domain.Contains(unique, id) {
			unique = append(unique, id)
		}
	}
	sort.Strings(unique)
	used = unique
	batch := domain.CommitBatch{CommitID: domain.NewID("cmt"), CommittedAt: domain.Stamp(now), Actor: domain.Actor{Kind: "principal", PrincipalID: &p, AuthorizationIDs: used}, Audit: domain.Audit{Action: method, RequestID: &requestID}, Events: events}
	if agent != "" {
		batch.Actor.AgentID = &agent
	}
	if reason, ok := params["reason"].(string); ok {
		batch.Audit.Reason = &reason
	}
	result := domain.CommandResult{Task: task, Records: records, CommitID: batch.CommitID, LeaseToken: token}
	if e := saneSize(result); e != nil {
		return nil, false, e
	}
	if e := saneSize(batch); e != nil {
		return nil, false, e
	}
	if e := tx.PutTask(task, previous); e != nil {
		return nil, false, e
	}
	for _, r := range records {
		if e := tx.PutRecord(r); e != nil {
			return nil, false, e
		}
	}
	if e := tx.AppendBatch(batch, task.TaskID); e != nil {
		return nil, false, e
	}
	payload, e := json.Marshal(result)
	if e != nil {
		return nil, false, e
	}
	encrypted := token != ""
	if encrypted {
		payload, e = s.box.Seal(payload, p+":"+key)
		if e != nil {
			return nil, false, e
		}
	}
	if e = tx.PutReceipt(ports.Receipt{Principal: p, Key: key, Hash: hash, TaskID: task.TaskID, AgentID: agent, Payload: payload, Encrypted: encrypted}); e != nil {
		return nil, false, e
	}
	if e = s.point("before_commit", method, key); e != nil {
		return nil, false, e
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	if e = s.point("after_commit_before_response", method, key); e != nil {
		return nil, false, e
	}
	return result, false, nil
}
