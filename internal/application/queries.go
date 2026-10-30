package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"

	"github.com/Csneer/agent-task-bridge/internal/auth"
	"github.com/Csneer/agent-task-bridge/internal/domain"
	"github.com/Csneer/agent-task-bridge/internal/ports"
	protocol "github.com/Csneer/agent-task-bridge/protocol/m1"
)

type scope struct {
	OwnerPrincipalID string `json:"owner_principal_id,omitempty"`
}
type cursor struct {
	Kind       string  `json:"kind"`
	Principal  string  `json:"principal"`
	Agent      string  `json:"agent"`
	Scope      scope   `json:"scope"`
	Epoch      int64   `json:"epoch"`
	ValidUntil *string `json:"valid_until"`
	Position   int64   `json:"position"`
	SnapshotID string  `json:"snapshot_id,omitempty"`
}
type snapshot struct {
	ID       string
	Cursor   cursor
	Records  []domain.Record
	Expires  time.Time
	Complete bool
}

func (s *Service) encodeCursor(c cursor) (string, error) {
	raw, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	sealed, e := s.cursors.Seal(raw, s.authority+":cursor-v1")
	return base64.RawURLEncoding.EncodeToString(sealed), e
}
func (s *Service) decodeCursor(encoded string) (cursor, error) {
	var c cursor
	raw, e := base64.RawURLEncoding.DecodeString(encoded)
	if e == nil {
		raw, e = s.cursors.Open(raw, s.authority+":cursor-v1")
	}
	if e == nil {
		e = json.Unmarshal(raw, &c)
	}
	if e != nil {
		return c, domain.E("INVALID_CURSOR")
	}
	return c, nil
}
func allGrants(tx ports.Transaction, tasks []domain.Task) ([]domain.Grant, error) {
	all := []domain.Grant{}
	for _, t := range tasks {
		gs, e := grants(tx, t.TaskID)
		if e != nil {
			return nil, e
		}
		all = append(all, gs...)
	}
	return all, nil
}
func scoped(t domain.Task, sc scope) bool {
	return sc.OwnerPrincipalID == "" || t.Contract.OwnerPrincipalID == sc.OwnerPrincipalID
}
func validateCursor(c cursor, kind, p, agent string, ep int64, now time.Time) error {
	if c.Kind != kind || c.Principal != p || c.Agent != agent || c.Position < 0 {
		return domain.E("INVALID_CURSOR")
	}
	if c.Epoch != ep || (c.ValidUntil != nil && !now.Before(domain.ParseTime(*c.ValidUntil))) {
		return domain.E("REINITIALIZE_REQUIRED")
	}
	return nil
}

func (s *Service) query(ctx context.Context, token, method string, params domain.Object) (any, bool, error) {
	tx, e := s.store.Begin(ctx, false)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback()
	now, e := s.now(tx, false)
	if e != nil {
		return nil, false, e
	}
	c, e := loadConfig(tx)
	if e != nil {
		return nil, false, e
	}
	p, e := authn(c, token, now)
	if e != nil {
		return nil, false, e
	}
	agent := domain.S(params, "as_agent_id")
	if agent != "" && auth.Binding(c, p, agent, now) == nil {
		return nil, false, domain.E("FORBIDDEN")
	}
	if method == "bridge.capabilities" {
		var schema map[string]any
		if e = json.Unmarshal(protocol.Schema, &schema); e != nil {
			return nil, false, e
		}
		defs := schema["$defs"].(map[string]any)
		caps := defs["Capabilities"].(map[string]any)["properties"].(map[string]any)
		names := []string{}
		for _, v := range caps["methods"].(map[string]any)["items"].(map[string]any)["enum"].([]any) {
			names = append(names, v.(string))
		}
		sort.Strings(names)
		limits := domain.Object{}
		for key, v := range defs["Limits"].(map[string]any)["properties"].(map[string]any) {
			limits[key] = v.(map[string]any)["const"]
		}
		return domain.Object{"authority_id": s.authority, "protocol_version": domain.Version, "methods": names, "limits": limits}, false, nil
	}
	if method == "receipt.get" {
		r, e := s.recover(tx, c, p, agent, domain.S(params, "idempotency_key"), "", now)
		if e != nil {
			return nil, false, e
		}
		if r == nil {
			return nil, false, domain.E("RECEIPT_NOT_FOUND")
		}
		return r, true, nil
	}
	if method == "task.get" || method == "record.get" {
		t, e := tx.Task(domain.S(params, "task_id"))
		if e != nil {
			return nil, false, e
		}
		gs, e := grants(tx, domain.S(params, "task_id"))
		if e != nil {
			return nil, false, e
		}
		if e = requireVisible(c, p, agent, t, gs, now); e != nil {
			return nil, false, e
		}
		if method == "task.get" {
			return t, false, nil
		}
		r, e := tx.Record(domain.S(params, "record_type"), domain.S(params, "record_id"))
		if e != nil {
			return nil, false, e
		}
		if r == nil || r.TaskID() != t.TaskID {
			return nil, false, domain.E("NOT_FOUND")
		}
		return r, false, nil
	}
	ep, e := epoch(tx)
	if e != nil {
		return nil, false, e
	}
	limit := int(domain.N(params, "limit"))
	switch method {
	case "task.discover":
		tasks, e := tx.Tasks()
		if e != nil {
			return nil, false, e
		}
		result := []domain.Task{}
		var next *string
		for _, t := range tasks {
			if t.TaskID <= domain.S(params, "after_task_id") || t.State != "offered" || t.RecoveryGate != "claimable" || t.CurrentAssignmentID != nil {
				continue
			}
			gs, e := grants(tx, t.TaskID)
			if e != nil {
				return nil, false, e
			}
			if !allowed(c, p, agent, t, gs, "task.read", now) || !allowed(c, p, agent, t, gs, "task.claim", now) {
				continue
			}
			if len(result) == limit {
				next = domain.Ptr(result[len(result)-1].TaskID)
				break
			}
			result = append(result, t)
		}
		return domain.Object{"tasks": result, "next_after_task_id": next}, false, nil
	case "events.open", "snapshot.open":
		sc, _ := domain.Decode[scope](params["scope"])
		tasks, e := tx.Tasks()
		if e != nil {
			return nil, false, e
		}
		gs, e := allGrants(tx, tasks)
		if e != nil {
			return nil, false, e
		}
		horizon := auth.Horizon(c, gs, now)
		base := cursor{Kind: "events", Principal: p, Agent: agent, Scope: sc, Epoch: ep, ValidUntil: horizon}
		if method == "events.open" {
			encoded, e := s.encodeCursor(base)
			return domain.Object{"cursor": encoded, "auth_valid_until": horizon}, false, e
		}
		records := []domain.Record{}
		bytes := 0
		for _, t := range tasks {
			if !scoped(t, sc) {
				continue
			}
			tg, e := grants(tx, t.TaskID)
			if e != nil {
				return nil, false, e
			}
			if !allowed(c, p, agent, t, tg, "task.read", now) {
				continue
			}
			rs, e := tx.Records(t.TaskID)
			if e != nil {
				return nil, false, e
			}
			rs = append(rs, domain.RecordOf("Task", t))
			for _, r := range rs {
				raw, e := json.Marshal(r)
				if e != nil {
					return nil, false, e
				}
				bytes += len(raw)
				if bytes > 33554432 || len(records) >= 10000 {
					return nil, false, domain.E("RESOURCE_LIMIT")
				}
				records = append(records, r)
			}
		}
		sort.Slice(records, func(i, j int) bool {
			x, y := records[i], records[j]
			if x.TaskID() != y.TaskID() {
				return x.TaskID() < y.TaskID()
			}
			if x.RecordType != y.RecordType {
				return x.RecordType < y.RecordType
			}
			return x.ID() < y.ID()
		})
		w, e := tx.Watermark()
		if e != nil {
			return nil, false, e
		}
		base.Position = w
		replay, e := s.encodeCursor(base)
		if e != nil {
			return nil, false, e
		}
		watermark := base
		watermark.Kind = "watermark"
		mark, e := s.encodeCursor(watermark)
		if e != nil {
			return nil, false, e
		}
		snap := &snapshot{ID: domain.NewID("snp"), Records: records, Expires: now.Add(300 * time.Second)}
		page := base
		page.Kind = "page"
		page.Position = 0
		page.SnapshotID = snap.ID
		snap.Cursor = page
		s.snapshotsMu.Lock()
		count := 0
		for id, old := range s.snapshots {
			if old.Complete || !now.Before(old.Expires) || old.Cursor.Epoch != ep || (old.Cursor.ValidUntil != nil && !now.Before(domain.ParseTime(*old.Cursor.ValidUntil))) {
				delete(s.snapshots, id)
			} else if old.Cursor.Principal == p {
				count++
			}
		}
		if count >= 2 || len(s.snapshots) >= 16 {
			s.snapshotsMu.Unlock()
			return nil, false, domain.E("RESOURCE_LIMIT")
		}
		s.snapshots[snap.ID] = snap
		s.snapshotsMu.Unlock()
		encoded, e := s.encodeCursor(page)
		if e != nil {
			return nil, false, e
		}
		return domain.Object{"snapshot_id": snap.ID, "watermark": mark, "replay_cursor": replay, "page_cursor": encoded, "expires_at": domain.Stamp(snap.Expires), "auth_valid_until": horizon}, false, nil
	case "snapshot.page":
		cur, e := s.decodeCursor(domain.S(params, "page_cursor"))
		if e != nil {
			return nil, false, e
		}
		if e = validateCursor(cur, "page", p, agent, ep, now); e != nil {
			return nil, false, e
		}
		if cur.SnapshotID != domain.S(params, "snapshot_id") {
			return nil, false, domain.E("INVALID_CURSOR")
		}
		s.snapshotsMu.Lock()
		defer s.snapshotsMu.Unlock()
		snap := s.snapshots[cur.SnapshotID]
		if snap == nil || !now.Before(snap.Expires) {
			return nil, false, domain.E("REINITIALIZE_REQUIRED")
		}
		if cur.Position > int64(len(snap.Records)) {
			return nil, false, domain.E("INVALID_CURSOR")
		}
		start := int(cur.Position)
		end := start + limit
		if end > len(snap.Records) {
			end = len(snap.Records)
		}
		records := append([]domain.Record{}, snap.Records[start:end]...)
		complete := end == len(snap.Records)
		var next *string
		if !complete {
			cur.Position = int64(end)
			encoded, e := s.encodeCursor(cur)
			if e != nil {
				return nil, false, e
			}
			next = &encoded
		} else {
			snap.Complete = true
		}
		return domain.Object{"records": records, "next_page_cursor": next, "complete": complete}, false, nil
	case "events.read":
		cur, e := s.decodeCursor(domain.S(params, "cursor"))
		if e != nil {
			return nil, false, e
		}
		if e = validateCursor(cur, "events", p, agent, ep, now); e != nil {
			return nil, false, e
		}
		w, e := tx.Watermark()
		if e != nil {
			return nil, false, e
		}
		if cur.Position > w {
			return nil, false, domain.E("REINITIALIZE_REQUIRED")
		}
		rows, e := tx.Batches(cur.Position, 1000)
		if e != nil {
			return nil, false, e
		}
		batches := []domain.CommitBatch{}
		for _, b := range rows {
			if b.Seq != cur.Position+1 {
				return nil, false, domain.E("REINITIALIZE_REQUIRED")
			}
			t, e := tx.Task(b.TaskID)
			if e != nil {
				return nil, false, e
			}
			visible := false
			if t != nil && scoped(*t, cur.Scope) {
				gs, e := grants(tx, t.TaskID)
				if e != nil {
					return nil, false, e
				}
				visible = allowed(c, p, agent, *t, gs, "task.read", now)
			}
			if visible && len(batches) >= limit {
				break
			}
			cur.Position = b.Seq
			if visible {
				batches = append(batches, b.Value)
			}
		}
		next, e := s.encodeCursor(cur)
		if e != nil {
			return nil, false, e
		}
		return domain.Object{"batches": batches, "next_cursor": next, "at_head": cur.Position == w}, false, nil
	}
	return nil, false, domain.E("METHOD_NOT_FOUND")
}
