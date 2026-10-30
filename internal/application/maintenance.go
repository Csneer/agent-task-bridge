package application

import (
	"agent-task-bridge/internal/auth"
	"agent-task-bridge/internal/domain"
	"context"
	"time"
)

func (s *Service) Maintain(ctx context.Context) error {
	tx, e := s.store.Begin(ctx, true)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = s.now(tx, true); e != nil {
		return e
	}
	tasks, e := tx.Tasks()
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	for _, t := range tasks {
		if t.CurrentAssignmentID != nil {
			if e = s.maintainTask(ctx, t.TaskID, "", ""); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Service) maintainTask(ctx context.Context, id, token, agent string) error {
	tx, e := s.store.Begin(ctx, true)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now, e := s.now(tx, true)
	if e != nil {
		return e
	}
	c, e := loadConfig(tx)
	if e != nil {
		return e
	}
	task, e := tx.Task(id)
	if e != nil {
		return e
	}
	if task == nil {
		return nil
	}
	if token != "" {
		p, e := authn(c, token, now)
		if e != nil {
			return e
		}
		gs, e := grants(tx, id)
		if e != nil {
			return e
		}
		if e = requireVisible(c, p, agent, task, gs, now); e != nil {
			return e
		}
	}
	if task.CurrentAssignmentID == nil {
		return nil
	}
	a, e := typedRecord[domain.Assignment](tx, "Assignment", *task.CurrentAssignmentID)
	if e != nil {
		return e
	}
	if !live(a) || a.Generation != task.Generation {
		return nil
	}
	state, reason, method := "", "", ""
	if !now.Before(domain.ParseTime(a.LeaseExpiresAt)) {
		state, reason, method = "expired", "lease_expired", "assignment.expire"
	} else if auth.BindingByID(c, a.BindingID, now) == nil {
		state, reason, method = "revoked", "authorization_revoked", "assignment.invalidate"
	}
	if state == "" {
		return nil
	}
	if task.Revision >= domain.MaxInteger {
		return domain.E("RESOURCE_LIMIT")
	}
	old := task.Revision
	task.Revision++
	task.UpdatedAt = domain.Stamp(now)
	events := []domain.Event{}
	records := []domain.Record{}
	emit := func(kind, event string, v any) {
		r := domain.RecordOf(kind, v)
		events = append(events, domain.Event{EventID: domain.NewID("evt"), Type: event, TaskID: task.TaskID, TaskRevision: task.Revision, RecordType: kind, Record: r.Record})
		if kind != "Task" {
			records = append(records, r)
		}
	}
	if e = endAssignment(tx, task, a, state, reason, now, emit); e != nil {
		return e
	}
	emit("Task", "task.reopened", task)
	if e = tx.PutTask(*task, old); e != nil {
		return e
	}
	for _, r := range records {
		if e = tx.PutRecord(r); e != nil {
			return e
		}
	}
	batch := domain.CommitBatch{CommitID: domain.NewID("cmt"), CommittedAt: domain.Stamp(now), Actor: domain.Actor{Kind: "authority", AuthorizationIDs: []string{}}, Audit: domain.Audit{Action: method, Reason: &reason}, Events: events}
	if e = saneSize(batch); e != nil {
		return e
	}
	if e = tx.AppendBatch(batch, task.TaskID); e != nil {
		return e
	}
	return tx.Commit()
}

// DurationUntil is for embedding supervisors; it never grants execution rights.
func DurationUntil(deadline string, now time.Time) time.Duration {
	return domain.ParseTime(deadline).Sub(now)
}
