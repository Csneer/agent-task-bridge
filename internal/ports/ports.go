// Package ports holds runtime inputs that have no transport or persistence policy.
package ports

import (
	"agent-task-bridge/internal/domain"
	"context"
	"time"
)

type HookEvent struct{ Point, Method, Key string }
type Hook func(HookEvent) error
type Clock func() time.Time

type Receipt struct {
	Principal, Key, Hash, TaskID, AgentID string
	Payload                               []byte
	Encrypted                             bool
}
type Batch struct {
	Seq    int64
	TaskID string
	Value  domain.CommitBatch
}
type Store interface {
	Begin(context.Context, bool) (Transaction, error)
	Close() error
}
type Transaction interface {
	Commit() error
	Rollback()
	Meta(string) (string, error)
	SetMeta(string, string) error
	Audit(string, string) error
	Task(string) (*domain.Task, error)
	Tasks() ([]domain.Task, error)
	PutTask(domain.Task, int64) error
	Record(string, string) (*domain.Record, error)
	Records(string) ([]domain.Record, error)
	PutRecord(domain.Record) error
	TokenHash(string) (string, error)
	PutTokenHash(string, string) error
	Receipt(string, string) (*Receipt, error)
	PutReceipt(Receipt) error
	AppendBatch(domain.CommitBatch, string) error
	Watermark() (int64, error)
	Batches(int64, int) ([]Batch, error)
}
