// Package application coordinates the authoritative M1 transactions.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"agent-task-bridge/internal/auth"
	"agent-task-bridge/internal/config"
	"agent-task-bridge/internal/domain"
	"agent-task-bridge/internal/ports"
	"agent-task-bridge/internal/secrets"
	"agent-task-bridge/internal/store/sqlite"
)

type Config = config.Config
type HookEvent = ports.HookEvent
type Options struct {
	Key                []byte
	Clock              func() time.Time
	Hook               func(HookEvent) error
	DisableMaintenance bool
}
type Service struct {
	store        ports.Store
	authority    string
	box, cursors *secrets.Box
	clock        func() time.Time
	hook         func(HookEvent) error
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	healthy      atomic.Bool
	timeMu       sync.Mutex
	lastTime     time.Time
	snapshotsMu  sync.Mutex
	snapshots    map[string]*snapshot
}

func Open(path string, cfg *Config, opts Options) (*Service, error) {
	box, e := secrets.New(opts.Key, "receipt")
	if e != nil {
		return nil, e
	}
	cursor, e := secrets.New(opts.Key, "cursor")
	if e != nil {
		return nil, e
	}
	st, e := sqlite.Open(path)
	if e != nil {
		return nil, e
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	s := &Service{store: st, box: box, cursors: cursor, clock: opts.Clock, hook: opts.Hook, snapshots: map[string]*snapshot{}}
	tx, e := st.Begin(context.Background(), false)
	if e != nil {
		st.Close()
		return nil, e
	}
	s.authority, e = tx.Meta("authority")
	lower, le := tx.Meta("clock_lower_bound")
	version, ve := tx.Meta("protocol_version")
	tx.Rollback()
	if e == nil {
		e = le
	}
	if e == nil {
		e = ve
	}
	if e == nil && s.authority != "" && version != domain.Version {
		e = errors.New("stored protocol version is not supported")
	}
	if lower != "" {
		s.lastTime = domain.ParseTime(lower)
	}
	if e != nil {
		st.Close()
		return nil, e
	}
	if cfg != nil {
		e = s.ImportConfig(context.Background(), *cfg)
	} else if s.authority == "" {
		e = errors.New("new store requires bootstrap config")
	}
	if e != nil {
		st.Close()
		return nil, e
	}
	if e = s.Maintain(context.Background()); e != nil {
		st.Close()
		return nil, e
	}
	s.healthy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	if !opts.DisableMaintenance {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					e := s.Maintain(ctx)
					s.healthy.Store(e == nil)
				}
			}
		}()
	}
	return s, nil
}
func (s *Service) AuthorityID() string { return s.authority }
func (s *Service) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return s.store.Close()
}
func (s *Service) point(point, method, key string) error {
	if s.hook != nil {
		return s.hook(HookEvent{Point: point, Method: method, Key: key})
	}
	return nil
}
func (s *Service) now(tx ports.Transaction, write bool) (time.Time, error) {
	s.timeMu.Lock()
	defer s.timeMu.Unlock()
	now := s.clock().UTC().Truncate(time.Millisecond)
	// A managed clock may jitter slightly; large backward steps fail closed.
	if now.Before(s.lastTime.Add(-time.Second)) {
		return time.Time{}, domain.E("UNAVAILABLE")
	}
	if now.Before(s.lastTime) {
		now = s.lastTime
	}
	s.lastTime = now
	if write {
		if e := tx.SetMeta("clock_lower_bound", domain.Stamp(now)); e != nil {
			return time.Time{}, e
		}
	}
	return now, nil
}
func loadConfig(tx ports.Transaction) (config.Config, error) {
	raw, e := tx.Meta("config")
	if e != nil {
		return config.Config{}, e
	}
	var c config.Config
	e = json.Unmarshal([]byte(raw), &c)
	return c, e
}
func epoch(tx ports.Transaction) (int64, error) {
	raw, e := tx.Meta("auth_epoch")
	if e != nil {
		return 0, e
	}
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}
func bumpEpoch(tx ports.Transaction) error {
	v, e := epoch(tx)
	if e != nil {
		return e
	}
	if v >= domain.MaxInteger {
		return domain.E("RESOURCE_LIMIT")
	}
	return tx.SetMeta("auth_epoch", strconv.FormatInt(v+1, 10))
}
func (s *Service) ImportConfig(ctx context.Context, cfg Config) error {
	tx, e := s.store.Begin(ctx, true)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now, e := s.now(tx, true)
	if e != nil {
		return e
	}
	existing, e := tx.Meta("config")
	if e != nil {
		return e
	}
	var prior *Config
	if existing != "" {
		old, e := loadConfig(tx)
		if e != nil {
			return e
		}
		prior = &old
	}
	if s.authority != "" && cfg.AuthorityID != s.authority {
		return errors.New("authority cannot be changed")
	}
	next, e := auth.PrepareImport(prior, cfg, now)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(next)
	if e != nil {
		return e
	}
	if string(raw) != existing {
		if e = tx.SetMeta("config", string(raw)); e != nil {
			return e
		}
		if e = tx.SetMeta("authority", next.AuthorityID); e != nil {
			return e
		}
		if e = tx.SetMeta("protocol_version", domain.Version); e != nil {
			return e
		}
		if e = bumpEpoch(tx); e != nil {
			return e
		}
		if e = tx.Audit(domain.Stamp(now), "configuration.imported"); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	// Authority is initialized before the service is exposed and never changes.
	if s.authority == "" {
		s.authority = next.AuthorityID
	}
	return nil
}

func grants(tx ports.Transaction, taskID string) ([]domain.Grant, error) {
	rs, e := tx.Records(taskID)
	if e != nil {
		return nil, e
	}
	out := []domain.Grant{}
	for _, r := range rs {
		if r.RecordType == "Grant" {
			v, e := domain.Decode[domain.Grant](r.Record)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
	}
	return out, nil
}
func typedRecord[T any](tx ports.Transaction, kind, id string) (*T, error) {
	r, e := tx.Record(kind, id)
	if e != nil || r == nil {
		return nil, e
	}
	v, e := domain.Decode[T](r.Record)
	return &v, e
}
func findRun(tx ports.Transaction, taskID, assignmentID string) (*domain.Run, error) {
	rs, e := tx.Records(taskID)
	if e != nil {
		return nil, e
	}
	for _, r := range rs {
		if r.RecordType == "Run" && domain.S(r.Record, "assignment_id") == assignmentID {
			v, e := domain.Decode[domain.Run](r.Record)
			return &v, e
		}
	}
	return nil, nil
}
func live(a *domain.Assignment) bool {
	return a != nil && (a.State == "claimed" || a.State == "active")
}
func allowed(c config.Config, p, a string, t domain.Task, gs []domain.Grant, action string, now time.Time) bool {
	ok, _ := auth.Can(c, p, a, t, gs, action, now)
	return ok
}
func authn(c config.Config, token string, now time.Time) (string, error) {
	p, _, e := auth.Authenticate(c, token, now)
	if errors.Is(e, auth.ErrUnauthenticated) {
		return "", domain.E("UNAUTHENTICATED")
	}
	return p, e
}
func errorOf(e error) *domain.Error {
	var de *domain.Error
	if errors.As(e, &de) {
		return de
	}
	return domain.E("INTERNAL_ERROR")
}
func requireVisible(c config.Config, p, a string, t *domain.Task, gs []domain.Grant, now time.Time) error {
	if t == nil {
		return domain.E("NOT_FOUND")
	}
	if allowed(c, p, a, *t, gs, "task.read", now) {
		return nil
	}
	// An invalid Agent context must not mask a target independently visible to
	// the authenticated Principal. This branch grants no Agent authority.
	if a != "" && auth.Binding(c, p, a, now) == nil && allowed(c, p, "", *t, gs, "task.read", now) {
		return domain.E("FORBIDDEN")
	}
	return domain.E("NOT_FOUND")
}
func saneSize(v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > 1048576 {
		return domain.E("RESOURCE_LIMIT")
	}
	return nil
}

// Leave enough room for the mandatory future Assignment/Run termination batch.
// Escaped JSON can be much larger than the accepted request bytes. Reserving
// 16 KiB covers bounded IDs, timestamps, three event envelopes and end fields.
func maintenanceRoom(task domain.Task) error {
	b, e := json.Marshal(task)
	if e != nil {
		return e
	}
	if len(b) > 1048576-16384 {
		return domain.E("RESOURCE_LIMIT")
	}
	return nil
}
func revision(t *domain.Task, expected int64) error {
	if t.Revision != expected {
		return domain.Stale(t.Revision)
	}
	if t.Revision >= domain.MaxInteger {
		return domain.E("RESOURCE_LIMIT")
	}
	return nil
}
func publicError(e error) string { return fmt.Sprint(errorOf(e).Data.Kind) }
