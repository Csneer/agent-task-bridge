// Package sqlite stores canonical facts, events and receipts in one transaction.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"agent-task-bridge/internal/domain"
	"agent-task-bridge/internal/ports"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }
type Tx struct {
	conn   *sql.Conn
	ctx    context.Context
	closed bool
}
type Receipt = ports.Receipt
type Batch = ports.Batch

func Open(path string) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, errors.New("a persistent database path is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version != 0 && version != 1 {
		db.Close()
		return nil, errors.New("unsupported database schema version")
	}
	_, err = db.Exec(`PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY, revision INTEGER NOT NULL, document TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS records(kind TEXT NOT NULL,id TEXT NOT NULL,task_id TEXT NOT NULL REFERENCES tasks(id),document TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE UNIQUE INDEX IF NOT EXISTS one_current_assignment ON records(task_id) WHERE kind='Assignment' AND json_extract(document,'$.state') IN ('claimed','active');
 CREATE UNIQUE INDEX IF NOT EXISTS one_run_per_assignment ON records(json_extract(document,'$.assignment_id')) WHERE kind='Run';
 CREATE UNIQUE INDEX IF NOT EXISTS one_submission_per_run ON records(json_extract(document,'$.run_id')) WHERE kind='Submission';
 CREATE UNIQUE INDEX IF NOT EXISTS one_decision_per_submission ON records(json_extract(document,'$.submission_id')) WHERE kind='Decision';
 CREATE INDEX IF NOT EXISTS records_task ON records(task_id,kind,id);
 CREATE TABLE IF NOT EXISTS batches(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,task_id TEXT NOT NULL REFERENCES tasks(id),document TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS receipts(principal TEXT NOT NULL,key TEXT NOT NULL,hash TEXT NOT NULL,task_id TEXT NOT NULL REFERENCES tasks(id),agent_id TEXT NOT NULL,payload BLOB NOT NULL,encrypted INTEGER NOT NULL,PRIMARY KEY(principal,key));
 CREATE TABLE IF NOT EXISTS assignment_secrets(assignment_id TEXT PRIMARY KEY,token_hash TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS admin_audit(seq INTEGER PRIMARY KEY AUTOINCREMENT,at TEXT NOT NULL,action TEXT NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Begin(ctx context.Context, write bool) (ports.Transaction, error) {
	c, e := s.db.Conn(ctx)
	if e != nil {
		return nil, domain.E("UNAVAILABLE")
	}
	query := "BEGIN"
	if write {
		query = "BEGIN IMMEDIATE"
	}
	if _, e = c.ExecContext(ctx, query); e != nil {
		c.Close()
		return nil, domain.E("UNAVAILABLE")
	}
	return &Tx{conn: c, ctx: ctx}, nil
}
func (t *Tx) Commit() error {
	if t.closed {
		return errors.New("transaction already ended")
	}
	_, e := t.conn.ExecContext(t.ctx, "COMMIT")
	if e != nil {
		_, _ = t.conn.ExecContext(context.Background(), "ROLLBACK")
	}
	t.closed = true
	t.conn.Close()
	return e
}
func (t *Tx) Rollback() {
	if !t.closed {
		_, _ = t.conn.ExecContext(context.Background(), "ROLLBACK")
		t.closed = true
		t.conn.Close()
	}
}
func (t *Tx) Meta(key string) (string, error) {
	var v string
	e := t.conn.QueryRowContext(t.ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return v, e
}
func (t *Tx) SetMeta(key, value string) error {
	_, e := t.conn.ExecContext(t.ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return e
}
func (t *Tx) Audit(at, action string) error {
	_, e := t.conn.ExecContext(t.ctx, "INSERT INTO admin_audit(at,action)VALUES(?,?)", at, action)
	return e
}
func (t *Tx) Task(id string) (*domain.Task, error) {
	var raw []byte
	e := t.conn.QueryRowContext(t.ctx, "SELECT document FROM tasks WHERE id=?", id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var task domain.Task
	e = json.Unmarshal(raw, &task)
	return &task, e
}
func (t *Tx) Tasks() ([]domain.Task, error) {
	rows, e := t.conn.QueryContext(t.ctx, "SELECT document FROM tasks ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		var raw []byte
		var v domain.Task
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (t *Tx) PutTask(task domain.Task, previous int64) error {
	raw, e := json.Marshal(task)
	if e != nil {
		return e
	}
	if previous == 0 {
		_, e = t.conn.ExecContext(t.ctx, "INSERT INTO tasks(id,revision,document)VALUES(?,?,?)", task.TaskID, task.Revision, raw)
		return e
	}
	r, e := t.conn.ExecContext(t.ctx, "UPDATE tasks SET revision=?,document=? WHERE id=? AND revision=?", task.Revision, raw, task.TaskID, previous)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		return fmt.Errorf("task CAS failed")
	}
	return e
}
func (t *Tx) Record(kind, id string) (*domain.Record, error) {
	var raw []byte
	e := t.conn.QueryRowContext(t.ctx, "SELECT document FROM records WHERE kind=? AND id=?", kind, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var data domain.Object
	e = json.Unmarshal(raw, &data)
	return &domain.Record{RecordType: kind, Record: data}, e
}
func (t *Tx) Records(taskID string) ([]domain.Record, error) {
	rows, e := t.conn.QueryContext(t.ctx, "SELECT kind,document FROM records WHERE task_id=? ORDER BY kind,id", taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Record{}
	for rows.Next() {
		var kind string
		var raw []byte
		if e = rows.Scan(&kind, &raw); e != nil {
			return nil, e
		}
		var o domain.Object
		if e = json.Unmarshal(raw, &o); e != nil {
			return nil, e
		}
		out = append(out, domain.Record{RecordType: kind, Record: o})
	}
	return out, rows.Err()
}
func (t *Tx) PutRecord(r domain.Record) error {
	b, e := json.Marshal(r.Record)
	if e != nil {
		return e
	}
	_, e = t.conn.ExecContext(t.ctx, "INSERT INTO records(kind,id,task_id,document)VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET document=excluded.document", r.RecordType, r.ID(), r.TaskID(), b)
	return e
}
func (t *Tx) TokenHash(id string) (string, error) {
	var s string
	e := t.conn.QueryRowContext(t.ctx, "SELECT token_hash FROM assignment_secrets WHERE assignment_id=?", id).Scan(&s)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return s, e
}
func (t *Tx) PutTokenHash(id, hash string) error {
	_, e := t.conn.ExecContext(t.ctx, "INSERT INTO assignment_secrets(assignment_id,token_hash)VALUES(?,?)", id, hash)
	return e
}
func (t *Tx) Receipt(principal, key string) (*Receipt, error) {
	r := Receipt{Principal: principal, Key: key}
	e := t.conn.QueryRowContext(t.ctx, "SELECT hash,task_id,agent_id,payload,encrypted FROM receipts WHERE principal=? AND key=?", principal, key).Scan(&r.Hash, &r.TaskID, &r.AgentID, &r.Payload, &r.Encrypted)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, e
}
func (t *Tx) PutReceipt(r Receipt) error {
	_, e := t.conn.ExecContext(t.ctx, "INSERT INTO receipts(principal,key,hash,task_id,agent_id,payload,encrypted)VALUES(?,?,?,?,?,?,?)", r.Principal, r.Key, r.Hash, r.TaskID, r.AgentID, r.Payload, r.Encrypted)
	return e
}
func (t *Tx) AppendBatch(b domain.CommitBatch, taskID string) error {
	raw, e := json.Marshal(b)
	if e != nil {
		return e
	}
	_, e = t.conn.ExecContext(t.ctx, "INSERT INTO batches(id,task_id,document)VALUES(?,?,?)", b.CommitID, taskID, raw)
	return e
}
func (t *Tx) Watermark() (int64, error) {
	var n int64
	e := t.conn.QueryRowContext(t.ctx, "SELECT COALESCE(MAX(seq),0) FROM batches").Scan(&n)
	return n, e
}
func (t *Tx) Batches(after int64, limit int) ([]Batch, error) {
	rows, e := t.conn.QueryContext(t.ctx, "SELECT seq,task_id,document FROM batches WHERE seq>? ORDER BY seq LIMIT ?", after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		var b Batch
		var raw []byte
		if e = rows.Scan(&b.Seq, &b.TaskID, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &b.Value); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
